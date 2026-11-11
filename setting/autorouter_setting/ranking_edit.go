package autorouter_setting

import (
	"maps"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/autorouter"
)

// RankingEntry 是手工编辑的榜单里的一行。Score 为 nil 表示这个模型在该画像里没有榜单分：
// 它不参与质量/价格比较，只在比不出结果时按列表顺序兜底。
type RankingEntry struct {
	Model string   `json:"model"`
	Score *float64 `json:"score"`
}

// RankingProfile 是一个画像的完整列表，顺序就是写进 task_models / default_models 的顺序。
type RankingProfile struct {
	Profile string         `json:"profile"`
	Models  []RankingEntry `json:"models"`
}

// RankingBase 是编辑者加载榜单时看到的 rank_sources 里的两个时间。保存时与当前榜单比较，
// 对不上说明期间被刷新、恢复内置或被别人改过，直接保存会把那次改动悄悄冲掉。
type RankingBase struct {
	GeneratedAt string `json:"generated_at"`
	EditedAt    string `json:"edited_at"`
}

// RankingEdit 是一次手工编辑通过校验后要写的选项，以及给审计用的改动概况。
type RankingEdit struct {
	// Options 是要在一个事务里写入的全部选项。
	Options map[string]string
	// Profiles 是列表有变化的画像；Added、Removed、Rescored 是这些画像里新增、移除、改了分的模型数，
	// 只调了顺序的画像也算有变化，三个数都不增加。
	Profiles                 []string
	Added, Removed, Rescored int
}

// rankingEditedAtKey 记在 rank_sources 里，表示这份榜单生成之后被手工改过。
// 刷新和恢复内置都会整个替换 rank_sources，这个标记随之消失。
const rankingEditedAtKey = "edited_at"

// Profiles 按 autorouter.RankingProfiles() 的顺序把榜单列成画像列表，就是手工编辑读到、
// 改完再交回 EditedRanking 的形状。general 用 DefaultModels；库里缺的画像给空列表。
func (r Ranking) Profiles() []RankingProfile {
	names := autorouter.RankingProfiles()
	profiles := make([]RankingProfile, 0, len(names))
	for _, name := range names {
		list := r.TaskModels[name]
		if name == autorouter.DefaultProfile {
			list = r.DefaultModels
		}
		entries := make([]RankingEntry, 0, len(list))
		for _, model := range list {
			entry := RankingEntry{Model: model}
			if score, scored := r.TaskScores[name][model]; scored {
				entry.Score = &score
			}
			entries = append(entries, entry)
		}
		profiles = append(profiles, RankingProfile{Profile: name, Models: entries})
	}
	return profiles
}

// EditedRanking 校验管理员手工编辑的整份榜单，返回要写入的选项。
//
//   - profiles 必须正好覆盖 autorouter.RankingProfiles() 里的每个画像一次。general 的列表写进
//     default_models 和 task_scores.general，不写进 task_models：写进去会多出一份与 default_models
//     重复的一级列表，两份不一致时说不清实际用的是哪份。
//   - general 不能清空，它是最后一层兜底；其它画像可以清空，清空后退到一级类的列表或 default_models。
//   - 模型名去掉首尾空白，同一画像里不能重复，不能是 auto；分数为 nil 或 0 到 1 之间的数。
//   - base 必须与当前榜单的 generated_at、edited_at 一致，见 RankingBase。
//   - rank_sources 照当前榜单的来源复制，再盖上编辑时间。存的榜单是旧分类下的时，当前榜单就是
//     内置的那份，复制来的 taxonomy_version 已经是当前的，所以保存后直接生效。
//   - 同时关掉定时刷新，否则下一次刷新会把手工改动整个覆盖掉。
//
// 内容与当前榜单完全一样时返回错误，免得一次空保存也盖上编辑时间、关掉刷新。
func (s *AutoRouterSetting) EditedRanking(base RankingBase, profiles []RankingProfile, now time.Time) (RankingEdit, error) {
	current := s.Ranking()
	if base.GeneratedAt != current.Sources["generated_at"] || base.EditedAt != current.Sources[rankingEditedAtKey] {
		return RankingEdit{}, common.NewMessage("The ranking was changed after you loaded it. Reload it and make your edits again.")
	}

	known := autorouter.RankingProfiles()
	taskModels := make(map[string][]string, len(known))
	taskScores := make(map[string]map[string]float64, len(known))
	var defaultModels []string
	var edit RankingEdit
	seen := make([]string, 0, len(known))
	for _, profile := range profiles {
		if !slices.Contains(known, profile.Profile) {
			return RankingEdit{}, common.NewMessage("Unknown ranking profile {{profile}}", map[string]any{"profile": profile.Profile})
		}
		if slices.Contains(seen, profile.Profile) {
			return RankingEdit{}, common.NewMessage("Profile {{profile}} is listed more than once", map[string]any{"profile": profile.Profile})
		}
		seen = append(seen, profile.Profile)

		models := make([]string, 0, len(profile.Models))
		scores := make(map[string]float64, len(profile.Models))
		for _, entry := range profile.Models {
			name := strings.TrimSpace(entry.Model)
			params := map[string]any{"profile": profile.Profile, "model": name}
			switch {
			case name == "":
				return RankingEdit{}, common.NewMessage("A model name in {{profile}} is empty", params)
			case strings.ContainsFunc(name, unicode.IsControl):
				return RankingEdit{}, common.NewMessage("A model name in {{profile}} contains a line break or control character", params)
			case name == autorouter.AutoModel:
				return RankingEdit{}, common.NewMessage("auto cannot be listed in a ranking")
			case slices.Contains(models, name):
				return RankingEdit{}, common.NewMessage("Model {{model}} is listed more than once in {{profile}}", params)
			}
			models = append(models, name)
			if entry.Score == nil {
				continue
			}
			// JSON 里写不出 NaN，这里照样挡住：NaN 会让质量/价格比较全部为假。
			if score := *entry.Score; math.IsNaN(score) || score < 0 || score > 1 {
				return RankingEdit{}, common.NewMessage("The score of {{model}} in {{profile}} must be between 0 and 1", params)
			}
			scores[name] = *entry.Score
		}

		oldModels := current.TaskModels[profile.Profile]
		if profile.Profile == autorouter.DefaultProfile {
			if len(models) == 0 {
				return RankingEdit{}, common.NewMessage("The general ranking cannot be empty, because it is the last fallback")
			}
			oldModels, defaultModels = current.DefaultModels, models
		} else {
			taskModels[profile.Profile] = models
		}
		taskScores[profile.Profile] = scores

		// 和当前榜单比，数出这个画像新增、移除、改了分的模型，只用于审计。
		oldScores := current.TaskScores[profile.Profile]
		added, removed, rescored := 0, 0, 0
		for _, name := range models {
			if !slices.Contains(oldModels, name) {
				added++
				continue
			}
			oldScore, hadScore := oldScores[name]
			newScore, hasScore := scores[name]
			if hadScore != hasScore || oldScore != newScore {
				rescored++
			}
		}
		for _, name := range oldModels {
			if !slices.Contains(models, name) {
				removed++
			}
		}
		if added+removed+rescored > 0 || !slices.Equal(models, oldModels) {
			edit.Profiles = append(edit.Profiles, profile.Profile)
			edit.Added += added
			edit.Removed += removed
			edit.Rescored += rescored
		}
	}
	for _, profile := range known {
		if !slices.Contains(seen, profile) {
			return RankingEdit{}, common.NewMessage("The ranking is missing profile {{profile}}. Reload it and make your edits again.", map[string]any{"profile": profile})
		}
	}
	if len(edit.Profiles) == 0 {
		return RankingEdit{}, common.NewMessage("No changes to save")
	}
	slices.Sort(edit.Profiles)

	sources := make(map[string]string, len(current.Sources)+1)
	maps.Copy(sources, current.Sources)
	sources[rankingEditedAtKey] = now.UTC().Format(time.RFC3339)
	options, err := encodeOptions(map[string]any{
		"autorouter_setting.task_models":    taskModels,
		"autorouter_setting.task_scores":    taskScores,
		"autorouter_setting.default_models": defaultModels,
		"autorouter_setting.rank_sources":   sources,
	})
	if err != nil {
		return RankingEdit{}, err
	}
	options["autorouter_setting.rank_refresh_enabled"] = "false"
	edit.Options = options
	return edit, nil
}
