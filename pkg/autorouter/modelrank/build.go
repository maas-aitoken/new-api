// Package modelrank builds the autorouter task → model mapping from public
// leaderboards: LiveBench, LMArena, the Artificial Analysis free API and
// Design Arena, with OpenRouter's model list as the candidate catalog. Each
// task type and task class is scored as the equal-weight mean of its metrics
// and routes to every covered model in score order; the cheapest default
// models on the score / price Pareto frontier lead the default list.
package modelrank

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/pkg/autorouter"
)

// Config holds the data sources and ranking parameters of a build.
type Config struct {
	OpenRouter string // OpenRouter API base, the candidate catalog
	// OpenRouterKey reads the Design Arena Elo OpenRouter relays when DAKey
	// is empty.
	OpenRouterKey string
	LiveBench     string // LiveBench site
	// LiveBenchRelease pins a release directory (YYYYMMDD); empty takes the
	// newest published one.
	LiveBenchRelease   string
	LMArena            string // LMArena leaderboard dataset base
	ArtificialAnalysis string // Artificial Analysis API base
	AAKey              string // Artificial Analysis API key
	DesignArena        string // Design Arena API base
	DAKey              string // Design Arena API key; empty reads it via OpenRouter
	MinContext         int    // minimum context length of a candidate
	// DefaultFloor is the share of the best default score the cheap leading
	// default models need.
	DefaultFloor float64
	// StaleProbe is how many of the newest candidates a source rates are
	// checked for each of its metrics; a metric none of them has is stale.
	StaleProbe int
	HTTPClient *http.Client
	// Logf receives progress and the leaderboard names left unmatched.
	Logf func(format string, args ...any)
}

// DefaultConfig returns the public sources and the ranking parameters the
// shipped mapping was built with.
func DefaultConfig() Config {
	return Config{
		OpenRouter:         "https://openrouter.ai/api/v1",
		LiveBench:          "https://livebench.ai",
		LMArena:            "https://hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset/resolve/main",
		ArtificialAnalysis: "https://artificialanalysis.ai/api/v2",
		DesignArena:        "https://www.designarena.ai/api/v1",
		MinContext:         128000,
		DefaultFloor:       0.9,
		StaleProbe:         10,
		HTTPClient:         &http.Client{Timeout: 120 * time.Second},
		Logf:               func(string, ...any) {},
	}
}

// Build fetches every source and ranks the candidates. It fails when any
// source fails or a profile ends up without a model, so a partial fetch never
// replaces a complete mapping.
func Build(ctx context.Context, cfg Config) (*Mapping, error) {
	src := &sources{client: cfg.HTTPClient, logf: cfg.Logf}
	pool, err := src.loadCandidates(ctx, cfg.OpenRouter, cfg.MinContext)
	if err != nil {
		return nil, fmt.Errorf("openrouter_models: %w", err)
	}
	cfg.Logf("candidates=%d", len(pool.list))

	meta := map[string]string{
		"generated_at":     time.Now().UTC().Format(time.RFC3339),
		"taxonomy_version": autorouter.TaxonomyVersion,
	}
	steps := []struct {
		name string
		load func() (string, error)
	}{
		{"livebench", func() (string, error) { return src.loadLiveBench(ctx, pool, cfg.LiveBench, cfg.LiveBenchRelease) }},
		{"lmarena", func() (string, error) { return src.loadLMArena(ctx, pool, cfg.LMArena) }},
		{"artificial_analysis", func() (string, error) {
			return src.loadArtificialAnalysis(ctx, pool, cfg.ArtificialAnalysis, cfg.AAKey)
		}},
		{"design_arena", func() (string, error) {
			switch {
			case cfg.DAKey != "":
				return src.loadDesignArena(ctx, pool, cfg.DesignArena, cfg.DAKey)
			case cfg.OpenRouterKey != "":
				return src.loadDesignArenaViaOpenRouter(ctx, pool, cfg.OpenRouter, cfg.OpenRouterKey)
			}
			return "", errors.New("needs a Design Arena or OpenRouter key")
		}},
	}
	for _, step := range steps {
		asOf, err := step.load()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", step.name, err)
		}
		meta[step.name] = asOf
	}
	for _, name := range slices.Sorted(maps.Keys(pool.matches)) {
		cfg.Logf("alias %s -> %s", name, pool.matches[name])
	}
	stale := pool.dropStale(cfg.StaleProbe)
	if len(stale) > 0 {
		cfg.Logf("stale metrics dropped: %v", stale)
		meta["stale_metrics"] = strings.Join(stale, " ")
	}
	pool.normalize()

	r := ranker{pool: pool, defaultFloor: cfg.DefaultFloor}
	r.score()
	m, err := r.mapping(meta)
	if err != nil {
		return nil, err
	}
	m.StaleMetrics = stale
	return m, nil
}
