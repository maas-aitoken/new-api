// Command modelrank writes the autorouter ranking with its full evidence to a
// file. The gateway refreshes the ranking itself on a schedule; this command
// regenerates the mapping embedded as the first-boot default and audits a
// ranking offline.
//
//	ARTIFICIAL_ANALYSIS_API_KEY=... DESIGN_ARENA_API_KEY=... go run ./pkg/autorouter/modelrank/cmd/modelrank
//	ARTIFICIAL_ANALYSIS_API_KEY=... OPENROUTER_API_KEY=... go run ./pkg/autorouter/modelrank/cmd/modelrank
//	go run ./pkg/autorouter/modelrank/cmd/modelrank -out /tmp/task_models.json -livebench-release 20261025
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter/modelrank"
)

func main() {
	cfg := modelrank.DefaultConfig()
	out := flag.String("out", "setting/autorouter_setting/task_models.json", "generated mapping file")
	aaKeyEnv := flag.String("aa-key-env", "ARTIFICIAL_ANALYSIS_API_KEY", "env var holding the Artificial Analysis key")
	daKeyEnv := flag.String("da-key-env", "DESIGN_ARENA_API_KEY", "env var holding the Design Arena key")
	orKeyEnv := flag.String("openrouter-key-env", "OPENROUTER_API_KEY", "env var holding the OpenRouter key, used for Design Arena without its own key")
	flag.StringVar(&cfg.OpenRouter, "openrouter", cfg.OpenRouter, "OpenRouter API base")
	flag.StringVar(&cfg.LiveBench, "livebench", cfg.LiveBench, "LiveBench site")
	flag.StringVar(&cfg.LiveBenchRelease, "livebench-release", "", "LiveBench release directory (YYYYMMDD), empty for the newest")
	flag.StringVar(&cfg.LMArena, "lmarena", cfg.LMArena, "LMArena leaderboard dataset base")
	flag.StringVar(&cfg.ArtificialAnalysis, "artificial-analysis", cfg.ArtificialAnalysis, "Artificial Analysis API base")
	flag.StringVar(&cfg.DesignArena, "design-arena", cfg.DesignArena, "Design Arena API base")
	flag.IntVar(&cfg.MinContext, "min-context", cfg.MinContext, "minimum context length of a candidate")
	flag.Float64Var(&cfg.DefaultFloor, "default-floor", cfg.DefaultFloor, "cheap leading default models score at least this share of the best default score")
	flag.IntVar(&cfg.StaleProbe, "stale-probe", cfg.StaleProbe, "newest candidates of a source checked for each of its metrics")
	flag.Parse()
	cfg.AAKey = os.Getenv(*aaKeyEnv)
	cfg.DAKey = os.Getenv(*daKeyEnv)
	cfg.OpenRouterKey = os.Getenv(*orKeyEnv)
	cfg.Logf = func(format string, args ...any) { fmt.Printf(format+"\n", args...) }

	mapping, err := modelrank.Build(context.Background(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	data, err := common.Marshal(mapping)
	if err != nil {
		log.Fatal(err)
	}
	if data, err = common.IndentJson(data); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	for _, profile := range slices.Sorted(maps.Keys(mapping.TaskModels)) {
		fmt.Printf("%-18s %d %v\n", profile, len(mapping.TaskModels[profile]), mapping.TaskModels[profile])
	}
	fmt.Printf("%-18s %d %v\nwrote %s\n", "default", len(mapping.DefaultModels), mapping.DefaultModels, *out)
}
