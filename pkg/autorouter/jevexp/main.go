// Command jevexp runs the autorouter jev classification offline against
// Langfuse observations exported as JSONL, to measure latency and label
// quality before anything is wired into the request path.
//
//	go run ./pkg/autorouter/jevexp -in observations.jsonl -out results.jsonl
//	go run ./pkg/autorouter/jevexp -in observations.jsonl -extract-only
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter"
)

type observation struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	StartTime string         `json:"startTime"`
	Input     any            `json:"input"`
	Metadata  map[string]any `json:"metadata"`
}

// item is one distinct (instruction, previous instruction) pair — the full
// classification input. Agent loops resend the same pair on every call, so
// classifying per distinct pair mirrors the per-turn cache the online router
// will use.
type item struct {
	Instruction    string   `json:"instruction"`
	Previous       string   `json:"previous,omitempty"`
	Agent          bool     `json:"agent"`
	HumanTurn      int      `json:"human_turn"`
	FirstSeen      string   `json:"first_seen"`
	ObservationIDs []string `json:"observation_ids"`
	SessionIDs     []string `json:"session_ids,omitempty"`

	Label            string `json:"label,omitempty"`
	Endpoint         string `json:"endpoint,omitempty"`
	QuestionsVersion string `json:"questions_version,omitempty"`
	StateRunes       int    `json:"state_runes,omitempty"`
	// LatencyMs is client wall time including network; ServerLatencyMs is
	// inference time when the endpoint reports it.
	LatencyMs       int64                `json:"latency_ms,omitempty"`
	ServerLatencyMs *float64             `json:"server_latency_ms,omitempty"`
	Decision        *autorouter.Decision `json:"decision,omitempty"`
	Request         json.RawMessage      `json:"request,omitempty"`
	Response        json.RawMessage      `json:"response,omitempty"`
	Error           string               `json:"error,omitempty"`
}

var whitespace = regexp.MustCompile(`\s+`)

// selfTrafficMarkers are the judge/review prompts of the automode data
// pipeline, which Langfuse recorded because they went through the gateway.
// Keep in sync with _SELF_TRAFFIC_MARKERS in automode-data-pipeline clean.py.
var selfTrafficMarkers = []string{
	"你是严格的评估员。根据当前完整对话，比较两条候选 assistant 下一步响应",
	"你是独立的判词复核员，不是投票员",
	"[pipeline-probe]",
}

func main() {
	in := flag.String("in", "", "Langfuse observations JSONL")
	out := flag.String("out", "jevexp_results.jsonl", "result JSONL")
	endpoint := flag.String("endpoint", os.Getenv("JEV_ENDPOINT"), "jev decisions endpoint, defaults to JEV_ENDPOINT")
	label := flag.String("label", "", "run label recorded on every result row")
	model := flag.String("model", os.Getenv("JEV_MODEL"), "jev model id, required by OpenRouter (e.g. typesafe/jev-1.13), defaults to JEV_MODEL")
	apiKeyEnv := flag.String("api-key-env", "JEV_API_KEY", "env var holding the bearer token, empty value sends none")
	maxRunes := flag.Int("max-runes", 2000, "content budget of current + previous instruction in runes")
	previousMaxRunes := flag.Int("previous-max-runes", 500, "cap of the previous instruction in runes")
	concurrency := flag.Int("concurrency", 1, "parallel jev calls; keep 1 for undistorted latency")
	timeout := flag.Duration("timeout", 60*time.Second, "per-call timeout")
	limit := flag.Int("limit", 0, "classify at most N instructions (0 = all)")
	extractOnly := flag.Bool("extract-only", false, "only extract instructions, do not call jev")
	flag.Parse()
	if *in == "" {
		log.Fatal("-in is required")
	}
	if *maxRunes > 0 && *previousMaxRunes >= *maxRunes {
		log.Fatal("-previous-max-runes must be smaller than -max-runes")
	}

	items, skipped, total, err := loadItems(*in)
	if err != nil {
		log.Fatal(err)
	}
	if *limit > 0 && len(items) > *limit {
		items = items[:*limit]
	}
	fmt.Printf("observations=%d distinct_inputs=%d skipped=%v\n", total, len(items), skipped)

	if !*extractOnly {
		client := &autorouter.JevClient{
			Endpoint:   *endpoint,
			Model:      *model,
			APIKey:     os.Getenv(*apiKeyEnv),
			HTTPClient: &http.Client{Timeout: *timeout},
		}
		for _, it := range items {
			it.Label = *label
			it.Endpoint = *endpoint
		}
		classifyAll(client, items, *maxRunes, *previousMaxRunes, *concurrency)
		printSummary(items)
	}
	if err := writeItems(*out, items); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s\n", *out)
}

func loadItems(path string) ([]*item, map[string]int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, err
	}
	defer file.Close()

	byText := map[string]*item{}
	skipped := map[string]int{}
	total := 0
	// Observation lines carry full conversations and can exceed several MB,
	// beyond bufio.Scanner's token limit.
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			total++
			var obs observation
			if err := common.Unmarshal(line, &obs); err != nil {
				return nil, nil, 0, fmt.Errorf("line %d: %w", total, err)
			}
			if reason := addObservation(byText, &obs); reason != "" {
				skipped[reason]++
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, nil, 0, readErr
		}
	}

	items := make([]*item, 0, len(byText))
	for _, it := range byText {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].FirstSeen < items[j].FirstSeen })
	return items, skipped, total, nil
}

// addObservation folds one observation into the distinct-instruction index
// and returns a skip reason when it contributes nothing.
func addObservation(byText map[string]*item, obs *observation) string {
	if obs.Type != "GENERATION" {
		return "not_generation"
	}
	var body []byte
	switch input := obs.Input.(type) {
	case string:
		body = []byte(input)
	case nil:
		return "no_input"
	default:
		encoded, err := common.Marshal(input)
		if err != nil {
			return "bad_input"
		}
		body = encoded
	}
	instruction := autorouter.ExtractInstruction(body)
	if instruction.Auxiliary {
		return "auxiliary"
	}
	if instruction.Text == "" {
		return "no_human_text"
	}
	for _, marker := range selfTrafficMarkers {
		if strings.HasPrefix(instruction.Text, marker) {
			return "pipeline_self_traffic"
		}
	}

	key := whitespace.ReplaceAllString(instruction.Text, " ") + "\x00" +
		whitespace.ReplaceAllString(instruction.Previous, " ")
	it, ok := byText[key]
	if !ok {
		it = &item{
			Instruction: instruction.Text,
			Previous:    instruction.Previous,
			Agent:       instruction.Agent,
			HumanTurn:   instruction.HumanTurns,
			FirstSeen:   obs.StartTime,
		}
		byText[key] = it
	}
	if obs.StartTime < it.FirstSeen {
		it.FirstSeen = obs.StartTime
	}
	it.ObservationIDs = append(it.ObservationIDs, obs.ID)
	if sessionID, _ := obs.Metadata["attributes.session.id"].(string); sessionID != "" &&
		!containsString(it.SessionIDs, sessionID) {
		it.SessionIDs = append(it.SessionIDs, sessionID)
	}
	return ""
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func classifyAll(client *autorouter.JevClient, items []*item, maxRunes, previousMaxRunes, concurrency int) {
	if concurrency < 1 {
		concurrency = 1
	}
	jobs := make(chan *item)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range jobs {
				state := autorouter.BuildState(it.Instruction, it.Previous, maxRunes, previousMaxRunes)
				it.QuestionsVersion = autorouter.QuestionsVersion
				it.StateRunes = 0
				for _, field := range state {
					it.StateRunes += len([]rune(field))
				}
				start := time.Now()
				resp, err := client.Classify(context.Background(), state)
				it.LatencyMs = time.Since(start).Milliseconds()
				if err != nil {
					it.Error = err.Error()
				} else {
					decision := resp.Decision(it.Previous != "")
					it.Decision = &decision
					it.ServerLatencyMs = resp.LatencyMs
					it.Request = resp.RequestBody
					it.Response = resp.ResponseBody
				}
				mu.Lock()
				done++
				fmt.Printf("[%d/%d] %dms %s\n", done, len(items), it.LatencyMs, progressLabel(it))
				mu.Unlock()
			}
		}()
	}
	for _, it := range items {
		jobs <- it
	}
	close(jobs)
	wg.Wait()
}

func progressLabel(it *item) string {
	if it.Error != "" {
		return "ERROR " + it.Error
	}
	return fmt.Sprintf("%s d=%d %q", it.Decision.TaskType, it.Decision.Difficulty, headRunes(it.Instruction, 40))
}

func headRunes(text string, n int) string {
	runes := []rune(whitespace.ReplaceAllString(text, " "))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:n]) + "…"
}

func printSummary(items []*item) {
	var latencies, serverLatencies []float64
	taskTypes := map[string]int{}
	difficulties := map[int]int{}
	taskChange, largerModel := 0, 0
	modes := map[string]int{}
	modeUnjudged, modePosition := 0, 0.0
	failed := 0
	for _, it := range items {
		if it.Error != "" {
			failed++
			continue
		}
		// The platform's tier weights do not matter here, only the judged shares and position.
		if mode := (autorouter.TierWeights{}).Blend(it.Decision.ModeProbabilities); mode.Reason != "" {
			modeUnjudged++
		} else {
			likeliest := autorouter.Preferences[0]
			for _, preference := range autorouter.Preferences {
				if mode.Shares[preference] > mode.Shares[likeliest] {
					likeliest = preference
				}
			}
			modes[likeliest]++
			modePosition += mode.Position
		}
		latencies = append(latencies, float64(it.LatencyMs))
		if it.ServerLatencyMs != nil {
			serverLatencies = append(serverLatencies, *it.ServerLatencyMs)
		}
		taskTypes[it.Decision.TaskType]++
		difficulties[it.Decision.Difficulty]++
		if p := it.Decision.TaskChange; p != nil && *p >= 0.5 {
			taskChange++
		}
		if p := it.Decision.LargerModelBenefit; p != nil && *p >= 0.5 {
			largerModel++
		}
	}
	fmt.Printf("classified=%d failed=%d\n", len(latencies), failed)
	if len(latencies) == 0 {
		return
	}
	sort.Float64s(latencies)
	fmt.Printf("latency_ms p50=%.0f p95=%.0f max=%.0f\n",
		percentile(latencies, 0.5), percentile(latencies, 0.95), latencies[len(latencies)-1])
	if len(serverLatencies) > 0 {
		sort.Float64s(serverLatencies)
		fmt.Printf("server_latency_ms p50=%.0f p95=%.0f max=%.0f\n", percentile(serverLatencies, 0.5),
			percentile(serverLatencies, 0.95), serverLatencies[len(serverLatencies)-1])
	}
	fmt.Printf("task_type=%v\ndifficulty=%v\ntask_change>=0.5: %d  larger_model_benefit>=0.5: %d\n",
		taskTypes, difficulties, taskChange, largerModel)
	// mode counts the likeliest preference of each judged item (a tie goes to the earlier
	// one); the mean position runs from 0 (capability) through 1 (balanced) to 2 (cost).
	meanPosition := "-"
	if judged := len(latencies) - modeUnjudged; judged > 0 {
		meanPosition = fmt.Sprintf("%.3f", modePosition/float64(judged))
	}
	fmt.Printf("mode=%v  no_usable_answer=%d  mean_position=%s\n", modes, modeUnjudged, meanPosition)
}

// percentile uses nearest-rank on sorted values.
func percentile(sorted []float64, p float64) float64 {
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(rank, 0)]
}

func writeItems(path string, items []*item) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, it := range items {
		line, err := common.Marshal(it)
		if err != nil {
			return err
		}
		writer.Write(line)
		writer.WriteByte('\n')
	}
	return writer.Flush()
}
