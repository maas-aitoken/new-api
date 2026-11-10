package modelrank

// scale says how a raw metric becomes a 0..1 score across the candidate pool.
type scale int

const (
	// ratio metrics are accuracies or indexes where zero means no capability:
	// value / best.
	ratio scale = iota
	// elo metrics are arena ratings: the expected score against the best model
	// relative to an even match, 2 / (1 + 10^((best-value)/400)).
	elo
	// spread metrics are signed arena coefficients: (value-worst) / (best-worst).
	spread
)

// metricScales lists every metric the generator collects. Keys are
// <source>.<metric>: lb = LiveBench, lma = LMArena, aa = Artificial Analysis,
// da = Design Arena.
var metricScales = map[string]scale{
	"lb.long_agentic":       ratio,
	"lb.coding":             ratio,
	"lb.browser":            ratio,
	"lb.data_analysis":      ratio,
	"lb.research":           ratio,
	"lb.design":             ratio,
	"lb.reasoning":          ratio,
	"aa.intelligence":       ratio,
	"aa.strategy_ops":       ratio,
	"da.website":            elo,
	"da.uicomponent":        elo,
	"da.slides":             elo,
	"lma.overall":           elo,
	"lma.coding":            elo,
	"lma.math":              elo,
	"lma.multi_turn":        elo,
	"lma.expert":            elo,
	"lma.creative_writing":  elo,
	"lma.writing_industry":  elo,
	"lma.business_industry": elo,
	"lma.factuality":        elo,
	"lma.webdev":            elo,
	"lma.document":          elo,
	"lma.search":            elo,
	"lma.agent_bash":        spread,
}

// visionMetrics need image input: a model that cannot take screenshots scores
// 0 on them for lack of input, not of skill, so its score is not recorded.
var visionMetrics = map[string]bool{"lb.browser": true}

// artificialAnalysisIndexes maps Artificial Analysis free-API evaluation fields
// to metrics.
var artificialAnalysisIndexes = map[string]string{
	"artificial_analysis_intelligence_index":     "aa.intelligence",
	"artificial_analysis_strategy_and_ops_index": "aa.strategy_ops",
}

// designArenaCategories maps Design Arena Models Arena categories to metrics.
var designArenaCategories = map[string]string{
	"website":     "da.website",
	"uicomponent": "da.uicomponent",
	"slides":      "da.slides",
}

// lmArenaBoard is one LMArena leaderboard config and the metric each of its
// categories feeds.
type lmArenaBoard struct {
	config     string
	categories map[string]string
}

var lmArenaBoards = []lmArenaBoard{
	{"text_style_control", map[string]string{
		"overall":          "lma.overall",
		"coding":           "lma.coding",
		"math":             "lma.math",
		"multi_turn":       "lma.multi_turn",
		"expert":           "lma.expert",
		"creative_writing": "lma.creative_writing",
		"industry_writing_and_literature_and_language":              "lma.writing_industry",
		"industry_business_and_management_and_financial_operations": "lma.business_industry",
	}},
	{"text_factuality", map[string]string{"overall": "lma.factuality"}},
	{"webdev", map[string]string{"webdev": "lma.webdev"}},
	{"document_style_control", map[string]string{"overall": "lma.document"}},
	{"search_style_control", map[string]string{"overall": "lma.search"}},
	{"agent_bash_reliability", map[string]string{"overall": "lma.agent_bash"}},
}

// DefaultProfile scores models for the default list. It is also the profile
// of the general class, whose ranking is the default list.
const DefaultProfile = "general"

// profiles are the metrics scoring each task type and task class, weighted
// equally. A model's score is the mean over the metrics it has data for.
var profiles = map[string][]string{
	"code":              {"lb.coding", "lb.long_agentic", "lma.agent_bash"},
	"code.build":        {"lb.coding", "lb.long_agentic", "lma.coding"},
	"code.frontend":     {"lma.webdev", "da.website", "da.uicomponent"},
	"code.ops":          {"lma.agent_bash", "lb.long_agentic"},
	"data":              {"lb.data_analysis", "aa.strategy_ops"},
	"data.analysis":     {"lb.data_analysis", "lma.math"},
	"data.business":     {"aa.strategy_ops", "lma.business_industry"},
	"general.search":    {"lma.search", "lma.factuality"},
	"general.research":  {"lb.research"},
	"general.browser":   {"lb.browser"},
	"general.writing":   {"lma.writing_industry", "lma.creative_writing", "lma.document"},
	"general.design":    {"lb.design", "da.slides"},
	"general.knowledge": {"lma.expert", "aa.intelligence"},
	"general.chat":      {"lma.overall", "lma.multi_turn"},
	DefaultProfile:      {"aa.intelligence", "lma.overall", "lb.reasoning"},
}
