package autorouter_setting

import (
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter"
)

const (
	optionPrefix = "autorouter_setting."
	// maxDifficultyRungs bounds difficulty_efforts; rungs past the last entry reuse it.
	maxDifficultyRungs = 8
	maxRunesLimit      = 100000
)

// ValidateOption checks a value before it is stored under an autorouter_setting.*
// key. Loading skips a value that does not parse, so without this check a typo
// would be saved and then never applied. Numbers are parsed the way the loader
// parses them, untrimmed.
func ValidateOption(key, value string) error {
	name, ok := strings.CutPrefix(key, optionPrefix)
	if !ok {
		return nil
	}
	switch name {
	case "enabled", "rank_refresh_enabled":
		if _, err := strconv.ParseBool(value); err != nil {
			return common.NewMessage("Must be true or false")
		}
	case "jev_timeout_ms":
		return integerInRange(value, 100, 60000)
	case "max_runes", "previous_max_runes":
		return integerInRange(value, 0, maxRunesLimit)
	case "cache_ttl_seconds":
		// 0 would make the cache entries never expire in Redis.
		return integerInRange(value, 1, 30*24*3600)
	case "rank_refresh_interval_hours":
		return integerInRange(value, 1, 720)
	case "upgrade_benefit", "min_task_type_probability", "continuation_task_change":
		return fractionInRange(value)
	case "default_effort":
		if !slices.Contains(autorouter.Efforts(), value) {
			return common.NewMessage("Unknown reasoning effort {{effort}}", map[string]any{"effort": value})
		}
	case "difficulty_efforts":
		var efforts []string
		if !decodeJSON(value, &efforts) || len(efforts) == 0 || len(efforts) > maxDifficultyRungs {
			return common.NewMessage("Must be a JSON array of 1 to {{max}} reasoning efforts", map[string]any{"max": maxDifficultyRungs})
		}
		known := autorouter.Efforts()
		for _, effort := range efforts {
			if !slices.Contains(known, effort) {
				return common.NewMessage("Unknown reasoning effort {{effort}}", map[string]any{"effort": effort})
			}
		}
	case "default_preference":
		if !slices.Contains(autorouter.SelectablePreferences, value) {
			return common.NewMessage("Unknown preference {{preference}}", map[string]any{"preference": value})
		}
	case "route_log_mode":
		if !slices.Contains(RouteLogModes, value) {
			return common.NewMessage("Unknown routing log mode {{mode}}", map[string]any{"mode": value})
		}
	case "route_log_retention_days":
		return integerInRange(value, 1, MaxRouteLogRetentionDays)
	case "cost_weights":
		var weights map[string]float64
		if !decodeJSON(value, &weights) {
			return common.NewMessage("Must be a JSON object that maps preferences to numbers between 0 and 1")
		}
		for preference, weight := range weights {
			if !slices.Contains(autorouter.Preferences, preference) {
				return common.NewMessage("Unknown preference {{preference}}", map[string]any{"preference": preference})
			}
			if weight < 0 || weight > 1 {
				return common.NewMessage("Must be a JSON object that maps preferences to numbers between 0 and 1")
			}
		}
	case "task_models":
		return decodesAs[map[string][]string](value)
	case "default_models":
		return decodesAs[[]string](value)
	case "rank_sources":
		return decodesAs[map[string]string](value)
	case "task_scores":
		var scores map[string]map[string]float64
		if !decodeJSON(value, &scores) {
			return common.NewMessage("The ranking data is not in the expected format")
		}
		for _, profile := range scores {
			for _, score := range profile {
				if score < 0 {
					return common.NewMessage("The ranking data is not in the expected format")
				}
			}
		}
	case "jev_endpoint":
		if value == "" {
			return nil
		}
		if err := plainText(value, 2048); err != nil {
			return err
		}
		parsed, err := url.ParseRequestURI(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return common.NewMessage("Must be an http or https URL")
		}
	case "jev_model":
		return plainText(value, 128)
	case "jev_api_key", "artificial_analysis_api_key", "design_arena_api_key", "openrouter_api_key":
		return plainText(value, 512)
	}
	return nil
}

func integerInRange(value string, low, high int) error {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number != math.Trunc(number) || number < float64(low) || number > float64(high) {
		return common.NewMessage("Must be an integer between {{min}} and {{max}}", map[string]any{"min": low, "max": high})
	}
	return nil
}

func fractionInRange(value string) error {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || number < 0 || number > 1 {
		return common.NewMessage("Must be a number between {{min}} and {{max}}", map[string]any{"min": 0, "max": 1})
	}
	return nil
}

// decodeJSON decodes value into target. A JSON null is refused: it decodes
// without error into a nil map or slice, which would load as an empty setting.
func decodeJSON(value string, target any) bool {
	return strings.TrimSpace(value) != "null" && common.UnmarshalJsonStr(value, target) == nil
}

func decodesAs[T any](value string) error {
	var decoded T
	if !decodeJSON(value, &decoded) {
		return common.NewMessage("The ranking data is not in the expected format")
	}
	return nil
}

// plainText accepts a value that goes into a URL or an HTTP header, so it
// must be a single token.
func plainText(value string, maxLength int) error {
	if len([]rune(value)) > maxLength {
		return common.NewMessage("Must be at most {{max}} characters", map[string]any{"max": maxLength})
	}
	if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return common.NewMessage("Must not contain spaces or line breaks")
	}
	return nil
}
