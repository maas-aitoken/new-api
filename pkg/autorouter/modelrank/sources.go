package modelrank

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/parquet-go/parquet-go"
)

type sources struct {
	client *http.Client
	logf   func(format string, args ...any)
}

// get makes up to attempts tries, retrying transient failures; a 429 waits
// for Retry-After, or 15s more per attempt. header is sent with every try.
func (s *sources) get(ctx context.Context, url string, header http.Header, attempts int) ([]byte, error) {
	var lastErr error
	wait := time.Duration(0)
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(max(wait, time.Duration(attempt)*2*time.Second)):
			}
			wait = 0
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		maps.Copy(req.Header, header)
		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GET %s: %s", url, resp.Status)
			if resp.StatusCode == http.StatusTooManyRequests {
				wait = time.Duration(attempt+1) * 15 * time.Second
				if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
					wait = time.Duration(seconds) * time.Second
				}
			}
			continue
		}
		return body, nil
	}
	return nil, lastErr
}

func (s *sources) getJSON(ctx context.Context, url string, header http.Header, v any) error {
	body, err := s.get(ctx, url, header, 5)
	if err != nil {
		return err
	}
	return common.Unmarshal(body, v)
}

type orModel struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug"`
	Created       int64  `json:"created"`
	ContextLength int    `json:"context_length"`
	Architecture  struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	Pricing struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
	SupportedParameters []string `json:"supported_parameters"`
	ExpirationDate      *string  `json:"expiration_date"`
	Reasoning           *struct {
		SupportedEfforts []string `json:"supported_efforts"`
	} `json:"reasoning"`
}

// loadCandidates keeps OpenRouter models usable as an agent backend: text in
// and out, tool calling, enough context, priced, not scheduled for removal.
// Routing aliases (~), variants (:free, :batch) and OpenRouter's own routers
// are dropped; ids sharing a canonical slug keep the shortest.
func (s *sources) loadCandidates(ctx context.Context, base string, minContext int) (*pool, error) {
	var resp struct {
		Data []orModel `json:"data"`
	}
	if err := s.getJSON(ctx, base+"/models", nil, &resp); err != nil {
		return nil, err
	}
	slices.SortFunc(resp.Data, func(a, b orModel) int {
		if d := len(a.ID) - len(b.ID); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	p := newPool()
	names := map[string]bool{}
	for _, m := range resp.Data {
		if strings.ContainsAny(m.ID, ":~") || strings.HasPrefix(m.ID, "openrouter/") ||
			!slices.Contains(m.SupportedParameters, "tools") ||
			!slices.Contains(m.Architecture.InputModalities, "text") ||
			!slices.Equal(m.Architecture.OutputModalities, []string{"text"}) ||
			m.ContextLength < minContext || m.ExpirationDate != nil {
			continue
		}
		price, err := blendedPrice(m.Pricing.Prompt, m.Pricing.Completion)
		if err != nil || price <= 0 {
			continue
		}
		name := gatewayName(m.ID)
		if p.bySlug[m.CanonicalSlug] != nil || names[name] {
			continue
		}
		names[name] = true
		c := &candidate{
			ID: m.ID, Slug: m.CanonicalSlug, Name: name, Price: price, Created: m.Created,
			Vision: slices.Contains(m.Architecture.InputModalities, "image"),
		}
		if m.Reasoning != nil {
			c.Efforts = m.Reasoning.SupportedEfforts
		}
		p.add(c)
	}
	return p, nil
}

var (
	liveBenchBundle  = regexp.MustCompile(`static/js/main\.[0-9a-f]+\.js`)
	liveBenchRelease = regexp.MustCompile(`"(20\d{6})"`)
)

// loadLiveBench reads the category averages of a LiveBench release; an empty
// release takes the newest one the site's bundle names that has a table.
func (s *sources) loadLiveBench(ctx context.Context, p *pool, site, release string) (string, error) {
	var body []byte
	var err error
	if release != "" {
		body, err = s.get(ctx, site+"/data/"+release+"/table.csv", nil, 5)
	} else {
		release, body, err = s.latestLiveBench(ctx, site)
	}
	if err != nil {
		return "", err
	}
	rows, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		return "", err
	}
	if len(rows) < 2 || rows[0][0] != "model" {
		return "", fmt.Errorf("unexpected LiveBench table header %v", rows[0])
	}
	var unmatched []string
	for _, row := range rows[1:] {
		c := p.match(row[0])
		if c == nil {
			unmatched = append(unmatched, row[0])
			continue
		}
		for i, column := range rows[0][1:] {
			metric := "lb." + column
			if _, ok := metricScales[metric]; !ok {
				continue
			}
			if v, err := strconv.ParseFloat(row[i+1], 64); err == nil {
				c.record(metric, v)
			}
		}
	}
	s.logf("livebench %s rows=%d unmatched=%v", release, len(rows)-1, unmatched)
	return release, nil
}

// latestLiveBench finds the newest release: the site publishes no index, but
// its bundle lists the release directories as "YYYYMMDD" strings.
func (s *sources) latestLiveBench(ctx context.Context, site string) (string, []byte, error) {
	index, err := s.get(ctx, site+"/", nil, 5)
	if err != nil {
		return "", nil, err
	}
	bundlePath := liveBenchBundle.Find(index)
	if bundlePath == nil {
		return "", nil, fmt.Errorf("livebench: no bundle in %s", site)
	}
	bundle, err := s.get(ctx, site+"/"+string(bundlePath), nil, 5)
	if err != nil {
		return "", nil, err
	}
	var releases []string
	for _, m := range liveBenchRelease.FindAllSubmatch(bundle, -1) {
		releases = append(releases, string(m[1]))
	}
	slices.Sort(releases)
	releases = slices.Compact(releases)
	slices.Reverse(releases)
	for _, release := range releases {
		if body, err := s.get(ctx, site+"/data/"+release+"/table.csv", nil, 1); err == nil {
			return release, body, nil
		}
	}
	return "", nil, fmt.Errorf("livebench: none of the bundle's releases %v has a table", releases)
}

type lmArenaRow struct {
	ModelName string   `parquet:"model_name"`
	Rating    *float64 `parquet:"rating,optional"`
	Score     *float64 `parquet:"score,optional"`
	Category  string   `parquet:"category"`
	Date      string   `parquet:"leaderboard_publish_date"`
}

// loadLMArena reads the latest snapshot of each LMArena board. Text boards
// carry a rating, agent boards a signed score.
func (s *sources) loadLMArena(ctx context.Context, p *pool, base string) (string, error) {
	var dates []string
	for _, board := range lmArenaBoards {
		body, err := s.get(ctx, base+"/"+board.config+"/latest-00000-of-00001.parquet", nil, 5)
		if err != nil {
			return "", err
		}
		rows, err := parquet.Read[lmArenaRow](bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return "", fmt.Errorf("%s: %w", board.config, err)
		}
		date := ""
		unmatched := map[string]bool{}
		for _, row := range rows {
			date = max(date, row.Date)
			metric, ok := board.categories[row.Category]
			if !ok {
				continue
			}
			value := row.Rating
			if value == nil {
				value = row.Score
			}
			if value == nil {
				continue
			}
			c := p.match(row.ModelName)
			if c == nil {
				unmatched[row.ModelName] = true
				continue
			}
			c.record(metric, *value)
		}
		s.logf("lmarena %s rows=%d unmatched=%d date=%s", board.config, len(rows), len(unmatched), date)
		dates = append(dates, board.config+"@"+date)
	}
	return strings.Join(dates, " "), nil
}

// loadArtificialAnalysis reads the index evaluations of every model on the
// free API, page by page. Models are matched by name, then by slug.
func (s *sources) loadArtificialAnalysis(ctx context.Context, p *pool, base, apiKey string) (string, error) {
	header := http.Header{"X-Api-Key": {apiKey}}
	version := 0
	rows, unmatched := 0, 0
	for page := 1; ; page++ {
		var resp struct {
			IntelligenceIndexVersion int `json:"intelligence_index_version"`
			Pagination               struct {
				HasMore bool `json:"has_more"`
			} `json:"pagination"`
			Data []struct {
				Name        string              `json:"name"`
				Slug        string              `json:"slug"`
				Evaluations map[string]*float64 `json:"evaluations"`
			} `json:"data"`
		}
		if err := s.getJSON(ctx, fmt.Sprintf("%s/language/models/free?page=%d", base, page), header, &resp); err != nil {
			return "", err
		}
		version = resp.IntelligenceIndexVersion
		for _, row := range resp.Data {
			rows++
			c := p.match(row.Name)
			if c == nil {
				c = p.match(row.Slug)
			}
			if c == nil {
				unmatched++
				continue
			}
			for field, metric := range artificialAnalysisIndexes {
				if v := row.Evaluations[field]; v != nil {
					c.record(metric, *v)
				}
			}
		}
		if !resp.Pagination.HasMore {
			break
		}
	}
	s.logf("artificial_analysis rows=%d unmatched=%d intelligence_index_version=%d", rows, unmatched, version)
	return fmt.Sprintf("intelligence_index_v%d", version), nil
}

// loadDesignArena reads the Models Arena Elo of each category. A row names
// its OpenRouter id when the model is on OpenRouter; other rows are matched by
// display name.
func (s *sources) loadDesignArena(ctx context.Context, p *pool, base, apiKey string) (string, error) {
	header := http.Header{"Authorization": {"Bearer " + apiKey}}
	var dates []string
	for _, category := range slices.Sorted(maps.Keys(designArenaCategories)) {
		var resp struct {
			Data []struct {
				DisplayName  string   `json:"displayName"`
				OpenRouterID *string  `json:"openRouterId"`
				Elo          *float64 `json:"elo"`
			} `json:"data"`
			Meta struct {
				LastUpdated *string `json:"lastUpdated"`
			} `json:"meta"`
		}
		if err := s.getJSON(ctx, base+"/leaderboard/models/"+category, header, &resp); err != nil {
			return "", err
		}
		unmatched := 0
		for _, row := range resp.Data {
			if row.Elo == nil {
				continue
			}
			var c *candidate
			if row.OpenRouterID != nil {
				c = p.bySlug[*row.OpenRouterID]
			}
			if c == nil {
				c = p.match(row.DisplayName)
			}
			if c == nil {
				unmatched++
				continue
			}
			c.record(designArenaCategories[category], *row.Elo)
		}
		date := ""
		if resp.Meta.LastUpdated != nil {
			date = *resp.Meta.LastUpdated
		}
		s.logf("design_arena %s rows=%d unmatched=%d updated=%s", category, len(resp.Data), unmatched, date)
		dates = append(dates, category+"@"+date)
	}
	return strings.Join(dates, " "), nil
}

// loadDesignArenaViaOpenRouter reads the Design Arena Models Arena Elo that
// OpenRouter's /benchmarks relays, keyed by canonical slug; it stands in for
// the Design Arena API when no Design Arena key is configured.
func (s *sources) loadDesignArenaViaOpenRouter(ctx context.Context, p *pool, base, apiKey string) (string, error) {
	var resp struct {
		Data []struct {
			Source   string   `json:"source"`
			Slug     string   `json:"model_permaslug"`
			Arena    string   `json:"arena"`
			Category string   `json:"category"`
			Elo      *float64 `json:"elo"`
		} `json:"data"`
		Meta struct {
			AsOf string `json:"as_of"`
		} `json:"meta"`
	}
	if err := s.getJSON(ctx, base+"/benchmarks", http.Header{"Authorization": {"Bearer " + apiKey}}, &resp); err != nil {
		return "", err
	}
	rows := map[string]int{}
	for _, row := range resp.Data {
		metric, ok := designArenaCategories[row.Category]
		if row.Source != "design-arena" || row.Arena != "models" || !ok || row.Elo == nil {
			continue
		}
		rows[row.Category]++
		if c := p.bySlug[row.Slug]; c != nil {
			c.record(metric, *row.Elo)
		}
	}
	s.logf("design_arena via openrouter rows=%v as_of=%s", rows, resp.Meta.AsOf)
	return "openrouter@" + resp.Meta.AsOf, nil
}
