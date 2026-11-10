package modelrank

import (
	"cmp"
	"fmt"
	"math"
	"slices"
)

type ranker struct {
	pool *pool
	// defaultFloor is the share of the best default score the cheap leading
	// default models need.
	defaultFloor float64
	// ranked holds, per profile, the models with enough coverage in descending
	// score order.
	ranked map[string][]*scored
}

type scored struct {
	c       *candidate
	Score   float64 // mean of the covered metrics
	Covered int     // metrics of the profile the model has data for
}

// minCovered is the number of a profile's metrics a model needs data for to
// be ranked under it: half of them, rounded up.
func minCovered(metrics int) int {
	return (metrics + 1) / 2
}

// score ranks every candidate under every profile. Ties go to the cheaper
// model, then to the id.
func (r *ranker) score() {
	r.ranked = map[string][]*scored{}
	for profile, metrics := range profiles {
		var list []*scored
		for _, c := range r.pool.list {
			s := &scored{c: c}
			for _, metric := range metrics {
				if v, ok := c.Norm[metric]; ok {
					s.Score += v
					s.Covered++
				}
			}
			if s.Covered < minCovered(len(metrics)) {
				continue
			}
			s.Score /= float64(s.Covered)
			list = append(list, s)
		}
		slices.SortFunc(list, func(a, b *scored) int {
			if c := cmp.Compare(b.Score, a.Score); c != 0 {
				return c
			}
			if c := cmp.Compare(a.c.Price, b.c.Price); c != 0 {
				return c
			}
			return cmp.Compare(a.c.ID, b.c.ID)
		})
		r.ranked[profile] = list
	}
}

// dominates reports whether a is at least as good as b on score and price and
// better on one of them.
func dominates(a, b *scored) bool {
	if a.Score < b.Score || a.c.Price > b.c.Price {
		return false
	}
	return a.Score > b.Score || a.c.Price < b.c.Price
}

// frontier returns the profile's models within defaultFloor of the best that
// are on the score / price Pareto frontier.
func (r *ranker) frontier(profile string) map[*candidate]bool {
	list := r.ranked[profile]
	on := map[*candidate]bool{}
	for _, s := range list {
		if s.Score < list[0].Score*r.defaultFloor {
			break
		}
		if !slices.ContainsFunc(list, func(o *scored) bool { return dominates(o, s) }) {
			on[s.c] = true
		}
	}
	return on
}

// Mapping is the generated ranking. rank_sources, task_models and
// default_models are autorouter setting values; the rest records how they
// were derived.
type Mapping struct {
	Sources       map[string]string     `json:"rank_sources"`
	Profiles      map[string][]string   `json:"profiles"`
	TaskModels    map[string][]string   `json:"task_models"`
	DefaultModels []string              `json:"default_models"`
	StaleMetrics  []string              `json:"stale_metrics,omitempty"`
	Evidence      map[string][]evidence `json:"evidence"`
	Models        map[string]modelInfo  `json:"models"`
}

type evidence struct {
	Model    string  `json:"model"`
	Score    float64 `json:"score"`
	Covered  int     `json:"covered"`
	Frontier bool    `json:"frontier,omitempty"`
}

type modelInfo struct {
	OpenRouterID string             `json:"openrouter_id"`
	Price        float64            `json:"price_per_mtok"`
	Efforts      []string           `json:"efforts,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
	Normalized   map[string]float64 `json:"normalized"`
}

func round(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

// mapping routes each profile to every model covering it in score order: the
// router takes the first one the caller can use, so whatever the deployment
// carries, a task lands on its best deployed model by its own ranking. Score
// order already puts every model after the ones dominating it. The default
// profile becomes the default list: its frontier, cheapest first, then every
// other model in score order.
func (r *ranker) mapping(sources map[string]string) (*Mapping, error) {
	m := &Mapping{
		Sources:    sources,
		Profiles:   profiles,
		TaskModels: map[string][]string{},
		Evidence:   map[string][]evidence{},
		Models:     map[string]modelInfo{},
	}
	for profile := range profiles {
		if len(r.ranked[profile]) == 0 {
			return nil, fmt.Errorf("profile %s has no model covering half of its metrics", profile)
		}
		if profile == DefaultProfile {
			continue
		}
		for _, s := range r.ranked[profile] {
			m.TaskModels[profile] = append(m.TaskModels[profile], s.c.Name)
		}
	}

	general := r.ranked[DefaultProfile]
	frontier := r.frontier(DefaultProfile)
	var defaults []*scored
	for _, s := range general {
		if frontier[s.c] {
			defaults = append(defaults, s)
		}
	}
	slices.SortStableFunc(defaults, func(a, b *scored) int { return cmp.Compare(a.c.Price, b.c.Price) })
	for _, s := range general {
		if !frontier[s.c] {
			defaults = append(defaults, s)
		}
	}
	for _, s := range defaults {
		m.DefaultModels = append(m.DefaultModels, s.c.Name)
	}

	for profile, list := range r.ranked {
		frontier := r.frontier(profile)
		for _, s := range list {
			m.Evidence[profile] = append(m.Evidence[profile], evidence{
				Model:    s.c.Name,
				Score:    round(s.Score),
				Covered:  s.Covered,
				Frontier: frontier[s.c],
			})
			info := modelInfo{
				OpenRouterID: s.c.ID,
				Price:        round(s.c.Price),
				Efforts:      s.c.Efforts,
				Metrics:      map[string]float64{},
				Normalized:   map[string]float64{},
			}
			for metric, v := range s.c.Raw {
				info.Metrics[metric] = round(v)
			}
			for metric, v := range s.c.Norm {
				info.Normalized[metric] = round(v)
			}
			m.Models[s.c.Name] = info
		}
	}
	return m, nil
}
