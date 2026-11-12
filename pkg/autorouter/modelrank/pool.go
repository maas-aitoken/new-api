package modelrank

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// candidate is one OpenRouter model the mapping may pick.
type candidate struct {
	ID   string // OpenRouter id, e.g. anthropic/claude-opus-5.5
	Slug string // canonical slug, e.g. anthropic/claude-opus-5.5-20260921
	// Name is the gateway model name: the OpenRouter id without vendor, in
	// the vendor's native spelling.
	Name    string
	Price   float64 // USD per 1M tokens at 3 prompt : 1 completion
	Efforts []string
	// Vision reports image input support; vision metrics need it.
	Vision bool
	// Created is when OpenRouter listed the model, in unix seconds.
	Created int64
	Raw     map[string]float64
	Norm    map[string]float64
}

type pool struct {
	list   []*candidate
	bySlug map[string]*candidate
	byKey  map[string]*candidate
	// matches records non-exact name matches for review.
	matches map[string]string
}

func newPool() *pool {
	return &pool{bySlug: map[string]*candidate{}, byKey: map[string]*candidate{}, matches: map[string]string{}}
}

func (p *pool) add(c *candidate) {
	c.Raw = map[string]float64{}
	p.list = append(p.list, c)
	p.bySlug[c.Slug] = c
	p.bySlug[c.ID] = c
	p.byKey[nameKey(c.ID)] = c
	// The slug key keeps the snapshot date, so dated leaderboard names
	// (DeepSeek-V4-Pro (High, 20260813)) reach that snapshot.
	if slugKey := nameKey(c.Slug); p.byKey[slugKey] == nil {
		p.byKey[slugKey] = c
	}
}

// gatewayName converts an OpenRouter id to the name vendors use on their own
// API: Anthropic spells versions with dashes (claude-opus-5-5), the others
// match OpenRouter.
func gatewayName(id string) string {
	vendor, name, _ := strings.Cut(id, "/")
	if vendor == "anthropic" {
		return strings.ReplaceAll(name, ".", "-")
	}
	return name
}

func blendedPrice(prompt, completion string) (float64, error) {
	p, err := strconv.ParseFloat(prompt, 64)
	if err != nil {
		return 0, err
	}
	c, err := strconv.ParseFloat(completion, 64)
	if err != nil {
		return 0, err
	}
	return (3*p + c) / 4 * 1e6, nil
}

var (
	parenthesized = regexp.MustCompile(`\([^)]*\)`)
	nonAlnum      = regexp.MustCompile(`[^a-z0-9]+`)
	// variantSuffix is a reasoning effort or snapshot date leaderboards append
	// to a model name. max is also a product tier (qwen3.8-max), so it is only
	// stripped on a second pass.
	variantSuffix    = regexp.MustCompile(`([-_ ](none|minimal|low|medium|high|xhigh|thinking|reasoning|non-reasoning|\d{8}|\d{4}-\d{2}-\d{2}))+$`)
	variantSuffixMax = regexp.MustCompile(`([-_ ](none|minimal|low|medium|high|xhigh|max|thinking|reasoning|non-reasoning|\d{8}|\d{4}-\d{2}-\d{2}))+$`)
	snapshotDate     = regexp.MustCompile(`\b(\d{8}|\d{4}-\d{2}-\d{2})\b`)
)

// releaseSuffixes are tails OpenRouter ids carry that leaderboard names omit.
var releaseSuffixes = []string{"preview", "it", "instruct", "0", "exp", "latest"}

func nameKey(name string) string {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name, _, _ = strings.Cut(name, ":")
	return nonAlnum.ReplaceAllString(name, "")
}

// match resolves a leaderboard model name to a candidate: the exact name
// first, then without parenthesized and effort/date suffixes (preferring the
// snapshot of a date the name carries), then with an OpenRouter release
// suffix appended to a name that had no effort to strip.
func (p *pool) match(name string) *candidate {
	if c := p.byKey[nameKey(name)]; c != nil {
		return c
	}
	var dates []string
	for _, date := range snapshotDate.FindAllString(name, -1) {
		dates = append(dates, strings.ReplaceAll(date, "-", ""))
	}
	base := strings.ToLower(strings.TrimSpace(parenthesized.ReplaceAllString(name, "")))
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	keys := []string{nameKey(base)}
	for _, re := range []*regexp.Regexp{variantSuffix, variantSuffixMax} {
		keys = append(keys, nameKey(strings.TrimSpace(re.ReplaceAllString(base, ""))))
	}
	found := func(c *candidate) *candidate {
		p.matches[name] = c.ID
		return c
	}
	for _, key := range slices.Compact(keys) {
		for _, date := range dates {
			if c := p.byKey[key+date]; c != nil {
				return found(c)
			}
		}
		if c := p.byKey[key]; c != nil {
			return found(c)
		}
	}
	for _, suffix := range releaseSuffixes {
		if c := p.byKey[keys[0]+suffix]; c != nil {
			return found(c)
		}
	}
	return nil
}

// record keeps the best value of a metric over a model's variants: boards
// list each reasoning effort separately and the router picks the effort.
func (c *candidate) record(metric string, value float64) {
	if _, ok := metricScales[metric]; !ok {
		panic(fmt.Sprintf("metric %s has no scale", metric))
	}
	if visionMetrics[metric] && !c.Vision {
		return
	}
	if old, ok := c.Raw[metric]; !ok || value > old {
		c.Raw[metric] = value
	}
}

// dropStale removes the metrics that stopped tracking new models: a metric is
// stale when none of the probe newest candidates its source rates has it.
// Vision metrics only probe candidates that take images. It returns the stale
// metrics in sorted order.
func (p *pool) dropStale(probe int) []string {
	newest := slices.Clone(p.list)
	slices.SortStableFunc(newest, func(a, b *candidate) int { return cmp.Compare(b.Created, a.Created) })
	var stale []string
	for _, metric := range slices.Sorted(maps.Keys(metricScales)) {
		source, _, _ := strings.Cut(metric, ".")
		rated, covered := 0, false
		for _, c := range newest {
			if rated == probe || covered {
				break
			}
			if !c.ratedBy(source) || visionMetrics[metric] && !c.Vision {
				continue
			}
			rated++
			_, covered = c.Raw[metric]
		}
		if covered || rated < probe {
			continue
		}
		stale = append(stale, metric)
		for _, c := range p.list {
			delete(c.Raw, metric)
		}
	}
	return stale
}

// ratedBy reports whether any metric of source has a value for the model.
func (c *candidate) ratedBy(source string) bool {
	for metric := range c.Raw {
		if strings.HasPrefix(metric, source+".") {
			return true
		}
	}
	return false
}

// normalize maps every metric to 0..1 across the candidates that have it.
func (p *pool) normalize() {
	for metric, sc := range metricScales {
		best, worst := math.Inf(-1), math.Inf(1)
		for _, c := range p.list {
			if v, ok := c.Raw[metric]; ok {
				best, worst = max(best, v), min(worst, v)
			}
		}
		for _, c := range p.list {
			v, ok := c.Raw[metric]
			if !ok {
				continue
			}
			if c.Norm == nil {
				c.Norm = map[string]float64{}
			}
			switch sc {
			case ratio:
				if best > 0 {
					c.Norm[metric] = max(v, 0) / best
				}
			case elo:
				c.Norm[metric] = 2 / (1 + math.Pow(10, (best-v)/400))
			case spread:
				if best > worst {
					c.Norm[metric] = (v - worst) / (best - worst)
				} else {
					c.Norm[metric] = 1
				}
			}
		}
	}
}
