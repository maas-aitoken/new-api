# 智能路由任务分类与模型榜单

`model: "auto"` 的请求由 jev 判断任务类型（`task_type`）和难度（`difficulty`），网关按任务类型从模型榜单里挑模型、按难度定思考强度。任务类型的唯一用途是选榜单，所以分类从榜单反推：先确定有哪些可信的公开榜单，再按"能用不同榜单区分"来划分任务。

## 1. 设计原则

- **每个任务类型必须落到榜单上**：一个类型对应 1–3 个直接测量该类工作的公开指标。找不到对应指标的类型不单独设立，归入上级或默认画像。
- **指标集相同就合并**：两个类型选出的指标完全一样时，它们的模型排序必然相同，分开只会增加 jev 的判错机会，因此合并为一个类型。
- **少而准，不凑权重**：每个画像最多 3 个指标，等权平均。不为"覆盖更全"加入只间接相关的指标。
- **场景聚焦互联网公司**：软件开发、运维自动化、数据与业务分析、写作、日常问答。法律、医疗、金融等行业专项不设类型。
- **只用独立第三方榜单**：LiveBench、LMArena、Artificial Analysis、Design Arena，OpenRouter 只作为模型目录（价格、上下文长度、工具调用支持），自身的评分、用量、延迟都不使用。

## 2. 数据来源

| 来源 | 地址 | 取用内容 | 指标前缀 |
| --- | --- | --- | --- |
| LiveBench | `https://livebench.ai/data/<release>/table.csv` | 最新版本的 7 个大类平均分 | `lb.` |
| LMArena | `hf-mirror.com/datasets/lmarena-ai/leaderboard-dataset` 下各榜单的 `latest` parquet | 文本竞技场分类 Elo、事实性、WebDev、文档、搜索、Agent bash 可靠性 | `lma.` |
| Artificial Analysis | `GET https://artificialanalysis.ai/api/v2/language/models/free`（`x-api-key`） | Intelligence 指数，Strategy & Ops 能力指数 | `aa.` |
| Design Arena | `GET https://www.designarena.ai/api/v1/leaderboard/models/{category}`（`Authorization: Bearer sk_live_...`） | Models Arena 的 `website`、`uicomponent`、`slides` 三个分类 Elo | `da.` |
| OpenRouter 模型列表 | `GET https://openrouter.ai/api/v1/models`（无需 key） | 候选模型、价格、上下文长度、`tools` 支持、可选思考强度 | — |

LMArena 镜像与 429 退避沿用现有实现。

**LiveBench 只读最新版本**：从官网首页 bundle 列出的版本中取最新一个存在 `table.csv` 的版本，不与旧版本合并；新版本没有重跑的模型视为无 LiveBench 数据。`browser` 需要截图输入，OpenRouter 目录中不支持图像输入的模型在该项记为无数据，不按 0 分计。

Artificial Analysis 免费接口只返回指数，不含单项 benchmark 分数；按 `name` / `slug` 走第 5 节的名称匹配。Design Arena 每行带 `openRouterId`，有值时直接对应候选模型，为 `null` 时按 `displayName` 匹配。

**Design Arena 的取数方式**：设置了 `DESIGN_ARENA_API_KEY` 时直连官方接口；未设置时改读 OpenRouter `GET /api/v1/benchmarks`（需 `OPENROUTER_API_KEY`）中 `source=design-arena`、`arena=models` 的行，按 `model_permaslug` 对应候选。OpenRouter 转发的数据不含 `slides`，此时 `da.slides` 被停更检测剔除，`general.design` 只按 `lb.design` 排序。`rank_sources.design_arena` 记为 `openrouter@<as_of>` 以示区分。

## 3. 指标目录

只采集被画像用到的指标。

| 指标键 | 来源与原始字段 | 测什么 | 刻度 |
| --- | --- | --- | --- |
| `lb.long_agentic` | LiveBench `long_agentic` | 多轮变更需求下扩展业务应用、在代码库上跑模拟数月的运维、长时间编码优化 | ratio |
| `lb.coding` | LiveBench `coding` | 在冻结的大型生产代码库上按 bug 报告修复真实缺陷，引入回归记 0 分 | ratio |
| `lb.browser` | LiveBench `browser` | 通过真实浏览器在商城、看板、工单、Wiki 等 Web 应用里完成多步操作 | ratio |
| `lb.data_analysis` | LiveBench `data_analysis` | 小表格上的连续事件识别、表连接列映射、CSV/JSON/Markdown 等格式互转 | ratio |
| `lb.research` | LiveBench `research` | 按真实业务简报调研并写出带来源的报告，按评分细则打分 | ratio |
| `lb.design` | LiveBench `design` | 按要求生成或改版 Word、PDF、PPT，渲染后按视觉与结构检查打分 | ratio |
| `lb.reasoning` | LiveBench `reasoning` | 逻辑、空间、心智理论短题 | ratio |
| `da.website` | Design Arena `models/website` | 按需求生成完整网页的人类偏好 | elo |
| `da.uicomponent` | Design Arena `models/uicomponent` | 生成单个 UI 组件的人类偏好 | elo |
| `da.slides` | Design Arena `models/slides` | 生成演示文稿的人类偏好 | elo |
| `aa.intelligence` | AA `artificial_analysis_intelligence_index` | 综合智力指数 | ratio |
| `aa.strategy_ops` | AA `artificial_analysis_strategy_and_ops_index` | 商业与管理知识、业务应用中的智能体工具使用、长上下文分析 | ratio |
| `lma.overall` | `text_style_control` / `overall` | 文本对话总体人类偏好 | elo |
| `lma.coding` | `text_style_control` / `coding` | 编程类对话偏好 | elo |
| `lma.math` | `text_style_control` / `math` | 数学类对话偏好 | elo |
| `lma.multi_turn` | `text_style_control` / `multi_turn` | 多轮对话 | elo |
| `lma.expert` | `text_style_control` / `expert` | 专家级难题 | elo |
| `lma.creative_writing` | `text_style_control` / `creative_writing` | 创意写作 | elo |
| `lma.writing_industry` | `text_style_control` / `industry_writing_and_literature_and_language` | 写作、文学与语言类职业场景 | elo |
| `lma.business_industry` | `text_style_control` / `industry_business_and_management_and_financial_operations` | 商业、管理与财务运营类职业场景 | elo |
| `lma.factuality` | `text_factuality` / `overall` | 事实准确性 | elo |
| `lma.webdev` | `webdev` / `webdev` | 前端页面生成的人类偏好 | elo |
| `lma.document` | `document_style_control` / `overall` | 基于上传文档作答 | elo |
| `lma.search` | `search_style_control` / `overall` | 联网搜索作答 | elo |
| `lma.agent_bash` | `agent_bash_reliability` / `overall` | Agent 执行 bash 的可靠性 | spread |

刻度含义见第 6.1 节。

## 4. 任务分类

两级分类：3 个一级类（`code`、`data`、`general`）、12 个二级类，外加 `unknown`。一级类参照 OpenRouter 任务分类的 `code:*`、`data:*` 与通用标签划分。jev 只在二级类和 `unknown` 中单选；一级类不单独提问，它的概率等于下属二级类概率之和。

每个一级类和二级类都有自己的画像（指标集），一级类画像用于二级类不可信或二级列表无可用模型时（见第 7 节）。`general` 的一级画像就是默认画像，对应 `default_models`。

### 4.1 分类与画像

| 一级 | 二级 | jev 判定标准 | 指标（等权） |
| --- | --- | --- | --- |
| `code` 代码 | — | — | `lb.coding`，`lb.long_agentic`，`lma.agent_bash` |
| | `code.build` | 编写或修改程序逻辑与配置：实现功能、按问题报告修复缺陷、审查代码，保证改动后程序行为正确 | `lb.coding`，`lb.long_agentic`，`lma.coding` |
| | `code.frontend` | 按需求开发或修改用户界面：网页、App、小程序、桌面客户端的页面、UI 组件、样式、交互与渲染性能，以用户看到的外观与操作体验为准 | `lma.webdev`，`da.website`，`da.uicomponent` |
| | `code.ops` | 在终端或服务器上执行命令，完成部署、环境配置、服务运维与线上故障排查 | `lma.agent_bash`，`lb.long_agentic` |
| `data` 数据 | — | — | `lb.data_analysis`，`aa.strategy_ops` |
| | `data.analysis` | 对给定的表格或数据求出确定的答案：统计、筛选、去重、关联、识别趋势，或在 CSV、JSON、Markdown 等格式间转换 | `lb.data_analysis`，`lma.math` |
| | `data.business` | 运用业务与管理知识做判断：分析经营或运营数据、比较方案、制定计划，给出可执行的商业决策建议 | `aa.strategy_ops`，`lma.business_industry` |
| `general` 通用 | — | — | `aa.intelligence`，`lma.overall`，`lb.reasoning` |
| | `general.search` | 联网查询一个具体问题的当前答案：天气、新闻、价格、版本发布、某个事实或链接，简短作答并给出出处 | `lma.search`，`lma.factuality` |
| | `general.research` | 围绕一个开放主题广泛检索多方资料，比较、核实并综合，产出带引用的调研结论或报告 | `lb.research` |
| | `general.browser` | 在网页应用里逐步点击、填写、核对，完成跨多个页面的操作任务，如汇总多页记录、批量修改订单、合并重复工单 | `lb.browser` |
| | `general.writing` | 产出或加工文字内容：撰写文案、文章、邮件、故事、诗歌等，或对给定文本总结、改写、润色、翻译 | `lma.writing_industry`，`lma.creative_writing`，`lma.document` |
| | `general.design` | 制作或改版成品文件：Word 文档、PDF、幻灯片的版式、排版、配色、图表与品牌样式 | `lb.design`，`da.slides` |
| | `general.knowledge` | 解释概念、原理或某领域的专业事实，说明某件事是什么、为什么或如何运作 | `lma.expert`，`aa.intelligence` |
| | `general.chat` | 日常对话与生活类求助：寒暄、闲聊、家常做法、个人化的推荐或经验分享 | `lma.overall`，`lma.multi_turn` |
| — | `unknown` | 离开前文完全无法判断要做什么：如"继续""ok""好的"、对前文方案的确认或选择、只用"这个""上面"指代前文且没有说明动作 | — |

### 4.2 划分依据

- **智能体按任务内容归类**：agent 是执行方式，不是任务内容。命令行与运维操作归 `code.ops`，浏览器操作归 `general.browser`，其余按所做的事归类。
- **`code.ops` 归入代码**：部署、配置、排障本质上是写命令和配置，`lma.agent_bash` 测的正是 agent 执行 bash 的可靠性。
- **`code.build` 同时覆盖开发、修复和审查**：公开榜单没有把这几类工作测出不同排序的指标，`lb.coding`（按 bug 报告修复真实缺陷）、`lb.long_agentic`（多轮变更需求下扩展应用）、`lma.coding`（编程对话的人类偏好）共同代表后端代码能力。
- **`code.frontend` 单列**：`lma.webdev`、`da.website`、`da.uicomponent` 都按人类对前端产出的偏好排序，与后端开发的排序差异明显。
- **`data.analysis` 与 `data.business` 分开**：前者是对数据本身的处理与计算，后者是在数据和信息上推理并给出决策（`aa.strategy_ops`）。
- **`general.search`、`general.research`、`general.knowledge` 分开**：`general.search` 是联网查一个确定问题的当前答案，`lma.search` 按人类对联网作答的偏好排序，`lma.factuality` 衡量事实是否答对；`general.research` 是多来源的长链路调研，由 LiveBench 2.0 的 `lb.research` 衡量；`general.knowledge` 不联网、凭模型知识解释，由 `lma.expert` 与 `aa.intelligence` 衡量。
- **`general.design` 用 `lb.design` 与 `da.slides`**：前者按渲染后的视觉与结构检查打分，后者是人类对幻灯片的偏好，两者都直接测量成品文档。
- **Design Arena 其余分类不用**：`gamedev`、`3d`、`svg`、`logo` 等不属于互联网公司的常规工作；`dataviz` 测的是图表页面生成，与 `data.analysis` 的统计、清洗、SQL 不是同一能力。
- **不用语言类榜单**：中文、英文等分榜按提问语言切分，测的不是任务能力，任何画像都不使用。
- **数学、逻辑题不单列**：互联网公司场景占比低，由 `general` 一级画像中的 `lb.reasoning` 覆盖。
- **difficulty、task_change、larger_model_benefit 三个问题不变。**
- **新增 `mode` 问题（问题集版本 v6）**：判断用户更看重结果质量与可靠（`capability`）、没有明显倾向（`balanced`），还是要省钱省时间（`cost`），返回三项的概率。判据只看用户这条消息本身，任务的难易不是依据，难度已经由 `difficulty` 管。每次调 jev 都问，不论令牌用哪种偏好，这样判断缓存里的每一条都带这个答案，令牌中途换成自适应偏好也不会拿到缺答案的缓存。问题集变了，缓存的判断随版本号失效一次。

## 5. 候选模型与名称匹配

候选集来自 OpenRouter 模型列表，条件不变：输入含文本、输出只有文本；支持 `tools`；上下文 ≥ 128k；无 `expiration_date`；有正价格；id 不含 `:`、`~`，不以 `openrouter/` 开头。多个 id 共用同一 canonical slug 时保留最短的。

网关模型名去掉厂商前缀并改为厂商原生写法（Anthropic 版本号的点换成横杠，如 `anthropic/claude-opus-5.5` → `claude-opus-5-5`）。价格按输入 : 输出 = 3 : 1 混合，单位 USD / 1M tokens。

LiveBench、LMArena、Artificial Analysis 的模型名统一走同一套匹配：精确匹配 → 去括号、思考强度、日期后缀 → 补 `preview`、`it`、`instruct` 等发布后缀。非精确匹配输出 `alias <榜单名> -> <OpenRouter id>`，未匹配输出 `unmatched`。同一模型的多个思考强度变体匹配到同一候选时，每项指标取最好的一个。

## 6. 打分

### 6.1 归一化

每项指标在拥有该指标的候选之间归一化到 0..1：

| 刻度 | 公式 |
| --- | --- |
| ratio | `value / best` |
| elo | `2 / (1 + 10^((best - value) / 400))` |
| spread | `(value - worst) / (best - worst)` |

### 6.2 画像得分

- **分数**：画像内有数据的指标取算术平均。
- **覆盖要求**：有数据的指标数 ≥ 画像指标数的一半（向上取整）。即 1–2 个指标的画像至少有 1 项，3 个指标的画像至少有 2 项。不满足的模型不进入该画像的列表。

分数只来自榜单，不叠加用量、延迟等其他信号。价格不进入分数，只在运行时和分数放在一起比较，见第 7.3 节。

## 7. 列表与运行时选择

### 7.1 列表

`task_models` 以画像名为键，共 14 个列表（12 个二级类 + `code`、`data` 两个一级类）。每个列表包含所有满足覆盖要求的模型，按分数降序；同分时价格低的在前，再相同按 id。列表不截断。运行时在当前可用的模型里按第 7.3 节比较质量和价格，比不出结果时取第一个可用的模型。

`default_models` 按 `general` 一级画像生成，同时充当 `general` 的一级列表：

1. 先放分数 ≥ 最高分 0.9 倍、且在分数/价格帕累托前沿上的模型，按价格升序；
2. 再放其余有 `general` 分数的模型，按分数降序。

运行时 `default_models` 也用 `general` 画像的榜单分，按第 7.3 节比较质量和价格；上面的顺序只在比较选不出结果时才起作用。

### 7.2 选择顺序

jev 返回各二级类的概率。设概率最高的二级类为 `leaf`，其一级类为 `parent`；一级类的概率为其下所有二级类概率之和，`best` 为概率最高的一级类：

| 条件 | 画像 |
| --- | --- |
| `leaf` 不是 `unknown` 且 `P(leaf) ≥ min_task_type_probability` | `leaf` |
| 否则 `P(best) ≥ min_task_type_probability` | `best` |
| 否则 | 沿用上一次路由；没有上一次时用 `default_models` |

确定画像后按 `task_models[leaf] → task_models[parent] → default_models` 依次取模型；从一级类开始时按 `task_models[best] → default_models`。`general` 没有一级列表，这一步即 `default_models`。每一级 `task_models` 列表先按第 7.3 节比较质量和价格；列表里没有带榜单分且可比价的可用模型时，取该列表第一个可用的模型；仍然没有才进入下一级。`default_models` 用 `general` 画像的榜单分，同样按第 7.3 节比较；比不出结果时取列表里第一个可用的模型。

可信的判断要求难度大于 0，来源可以是现调 jev、判断缓存命中、延续上一任务，或可信的会话继承。可信的判断自己定思考强度，按模型的渠道声明收敛后写进请求体，覆盖客户端写的推理字段；只有一级类可信时同样如此。模型没有推理声明时不动客户端的字段。不可信的兜底（辅助请求、jev 失败、概率不够）沿用上一次路由或 `default_models`，不碰客户端的推理字段。

思考强度、`larger_model_benefit` 提档、同任务延续（`task_change < continuation_task_change` 时沿用上一次的模型、思考强度不降）的规则不变。`ResolveVariant` 解析思考强度变体的规则不变，只补了一处：档位不在档位表里时也探测 `-medium` 后缀。“可用”的含义见第 7.3 节。

路由日志与 trace 记录 `task_type`（jev 选中的二级类）、`route_profile`（实际使用的画像）和 `route_fallback`（`leaf` / `class` / `default`），用于复核分类和兜底的发生频率。消费日志的 `admin_info.auto_router` 带完整的决策过程，包括第 7.3 节的比较明细，成功和失败的请求都写，样例见第 7.4 节。一次请求是怎么一步步走到最后那个模型的，另存在选路日志里，见第 7.5 节。

### 7.3 质量与价格的比较

榜单分只说明谁更强，不说明谁更划算：分数接近的模型，价格可以差好几倍。所以每一级 `task_models` 列表和 `default_models` 都把分数和价格放在一起比一次，而不是直接取第一名。这是对整个列表做一次整体比较，不是从第一名开始逐个往下比价。

**偏好**：比较时愿意为价格让出多少榜单分，由令牌上的 auto 偏好决定。偏好有三档，各对应一个成本权重 w：

| 偏好 | 含义 | 默认 w |
| --- | --- | --- |
| `capability` 能力优先 | 不为价格让分，取榜单分最高的 | 0 |
| `balanced` 均衡 | 为明显更低的价格让出一点分 | 0.07 |
| `cost` 成本优先 | 为低价让出较多的分 | 0.3 |

令牌没设偏好（或值不认识）时用平台的 `default_preference`，出厂是 `balanced`；它也不认识时用 `balanced`。这个偏好只决定 auto 选哪个模型，和 Scheduler 的路由倾向（决定选定的模型走哪个渠道）互不相干，两边不读对方的设置。

**自适应偏好**：令牌和 `default_preference` 还可以选 `adaptive`（界面上叫“按请求判断”）。它没有固定的 w，每个请求用 jev 的 `mode` 判断，从上面三档的 w 里折算出这个请求的 w，再按下面的步骤比较。`cost_weights` 仍只有三个键，写 `adaptive` 会被拒绝。

折算方法：三档记为位置 0（`capability`）、1（`balanced`）、2（`cost`），先把 `mode` 给三项的概率归一，对位置求期望，再沿 (0, w能力)、(1, w均衡)、(2, w成本) 连成的折线读出 w。

| 能力 / 均衡 / 成本 | 期望位置 | w |
| --- | --- | --- |
| 0 / 1 / 0 | 1 | 0.0700 |
| 0.1 / 0.6 / 0.3 | 1.2 | 0.1160 |
| 0.3 / 0.6 / 0.1 | 0.8 | 0.0560 |
| 1/3 / 1/3 / 1/3 | 1 | 0.0700 |
| 0.5 / 0 / 0.5 | 1 | 0.0700 |
| 0 / 0 / 1 | 2 | 0.3000 |

不直接对三档的 w 求平均，是因为默认的 0 / 0.07 / 0.3 间距不均：直接平均时三项各占三分之一得 0.123，一半能力一半成本得 0.15，拿不准的判断会偏向成本。这样折算后，判断完全确定时就是该档的 w，对称的不确定落在均衡档，概率从能力移向成本时 w 不会变小。三档的 w 在间距均匀时两种算法的结果相同；代价是只看期望位置，“一半能力一半成本”和“全是均衡”得到同一个 w。折线假定三档的 w 按能力、均衡、成本递增，界面和接口目前不强制这一点。

没有可用的 `mode` 答案（没有判断、答案缺失、概率含负数或 NaN、总和不为正）时用均衡档的 w，选路日志里 `mode.reason` 是 `no_answer`。`mode` 与任务类型是否可信无关：任务类型不可信、改走默认模型时，成本倾向照样用。沿用上一轮模型的请求不做比较，w 只记录不使用。w 每次请求用缓存里的概率和当前的 `cost_weights` 重新算，平台改了三档的 w，已缓存的判断立刻按新值读。

**候选**：列表里同时满足下面三条的模型。

- 在该画像下有榜单分（`task_scores`）。
- 当前可用：令牌白名单允许、所在分组启用，并且至少有一个渠道可用（已启用、支持当前请求路径、没被熔断，渠道的生效能力满足请求）。判断时撇开客户端自己写的推理档位，因为可信的判断会自己定档位。
- 有可比较的价格：按 token 计价，且用户侧价格已配置。

没配置价格的模型计费时会直接失败，任何路径都不选。按次计价的模型可以被选中，但没法和按 token 计价的模型比价，所以不进入比较，只会在比较选不出结果时按列表顺序出现。

**步骤**（w 是当前偏好的成本权重）：

1. 参照分：候选里最高的榜单分。它是当前可用候选的最高分，不一定是榜单第一名。
2. 候选带：榜单分不低于 `参照分 − w` 的候选。再便宜，掉出这条线的也不选。
3. 价格折扣：带内按价格的对数位置打折，最便宜的折 0，最贵的折满 w，其余按对数位置插值。免费（价格为 0）的模型不拉低价格下限；带内全部免费或价格相同时不打折。
4. 折后分：榜单分减折扣。
5. 选择：折后分最高的中选；折后分相同取价格更低的，再相同取榜单靠前的。

w 既是候选带的宽度，也是折扣的上限，所以选中的模型比参照分最多低 w。w 为 0 时候选带只剩与参照分并列的模型，等于取榜单分最高的，并列时取更便宜的。

**参数**：`autorouter_setting.cost_weights`（偏好 → w，默认 `capability` 0、`balanced` 0.07、`cost` 0.3）和 `default_preference`（默认 `balanced`）。两者在库里，改动即时生效，不需要发版；`cost_weights` 里没写的偏好用内置值，负数和 NaN 按 0，大于 1 按 1。w 对所有画像相同，没有按画像的设置；按令牌设的只有偏好这一档。

**价格**：用户侧的混合价（美元/百万 token），输入价和输出价按 3:1 混合，再乘计费分组倍率。倍率计价的模型取模型倍率和补全倍率；表达式计价的模型把预估输入长度代入表达式，取对应档位的价格，预估不出长度时按最小档。令牌分组是 `auto` 时，每个候选按自己实际会走的计费分组取倍率。这个价格不是 `task_models.json` 里的 `price_per_mtok`，后者是 OpenRouter 的价格，只用来给 `default_models` 排序和生成榜单。

**不做比较的情况**：

- 同任务延续，沿用上一次的模型。所以对话中途换偏好，要到下一个新任务才生效。
- 辅助请求、jev 失败或判断不可信时，上一次的模型仍可用就沿用它；没有可沿用的，才按 `general` 画像的榜单分比较 `default_models`。
- 该级列表没有带榜单分的可用模型，或者没有可比价的候选，取列表第一个可用的。

调用 jev 之前，网关先扫一遍 `default_models` 和各 `task_models`：一个可用模型都没有就直接返回 503，不花这次 jev 调用；候选是被能力门控全部淘汰的，错误信息里会列出未满足的能力维度。

### 7.4 示例

下面的数字取自 2026-10-28 的内置榜单，由实际的选择代码算出。价格用 `task_models.json` 里的 `price_per_mtok` 代替用户侧价格，只用来演示机制；线上的分数和价格以当时的榜单和计费配置为准。没有特别说明时用均衡档（w=0.07），分数和折扣保留四位小数。

**例 1：同一个请求，三档偏好选出三个模型**

jev 判成 `code.build`，概率 0.82，叶子可信，所有模型都可用。榜单第一名是 `claude-fable-5-1`（0.9865 分，20 美元）。

- 能力优先（w=0）：候选带只剩并列最高分的模型，选 `claude-fable-5-1`。
- 均衡（w=0.07）：候选带的下限是 0.9865 − 0.07 = 0.9165。`claude-sonnet-5-5`（0.9052）和 `gemini-4-argon`（0.9012）低于下限，不参与。带内 4 个模型：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-fable-5-1 | 0.9865 | 20.00 | 0.0700 | 0.9165 |
| gpt-6-astra | 0.9765 | 20.00 | 0.0700 | 0.9065 |
| claude-opus-5-5 | 0.9700 | 8.00 | 0.0301 | 0.9399 |
| gpt-6.1-sol | 0.9410 | 4.00 | 0 | 0.9410 |

价格最贵的 20 美元折满 0.07，最便宜的 4 美元折 0，8 美元按对数位置折 0.0301。折后分最高的是 `gpt-6.1-sol`（0.9410）：榜单分比第一名低 0.0455，价格是五分之一，没有超过 w。它只比 `claude-opus-5-5`（0.9399）高 0.0011，这是个近乎并列的结果，见第 10 节。

- 成本优先（w=0.3）：候选带的下限是 0.6865，带内 15 个模型。`claude-haiku-5-5`（0.7536 分，0.20 美元）是带内最便宜的，折扣为 0，折后分 0.7536，高于 `gpt-6.1-sol` 的 0.7458（0.9410 − 0.1952），中选。它的榜单分比第一名低 0.2329，价格是百分之一。

**例 2：榜单第一名本身就便宜，三档都选它**

jev 判成 `code.ops`，概率 0.90。第一名 `claude-sonnet-5`（0.9945 分）只要 4 美元。成本优先（w=0.3）时候选带的下限是 0.6945，带内 25 个模型，下面是其中 5 个：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-sonnet-5 | 0.9945 | 4.00 | 0.1981 | 0.7964 |
| beam-1.0-501b-a23b | 0.8065 | 0.44 | 0.0580 | 0.7485 |
| deepseek-v4.1-flash | 0.7264 | 0.21 | 0.0115 | 0.7149 |
| gemini-3.7-flash | 0.8462 | 1.50 | 0.1360 | 0.7102 |
| mimo-v2.6-flash | 0.6986 | 0.17 | 0 | 0.6986 |

`mimo-v2.6-flash`（0.17 美元）是带内最便宜的，折扣为 0，折后分只有 0.6986。`claude-sonnet-5` 比第二名 `claude-opus-5-5` 高 0.0272，比 `beam-1.0-501b-a23b` 高 0.1880，价格折扣补不上这个分差，所以能力优先、均衡、成本优先三档都选它。成本优先不等于永远选最便宜的。

**例 3：叶子不可信，按大类选**

jev 的概率是 `general.chat` 0.40、`code.build` 0.35、`code.ops` 0.25。概率最高的叶子只有 0.40，没到 0.5；`code` 大类是 0.35 + 0.25 = 0.60，达到阈值，所以按 `code` 画像选，在 `task_models["code"]` 里比较。均衡档的参照分是 0.9734，下限 0.9034，带内 4 个模型：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-opus-5-5 | 0.9734 | 8.00 | 0.0301 | 0.9433 |
| claude-fable-5-1 | 0.9703 | 20.00 | 0.0700 | 0.9003 |
| gpt-6-astra | 0.9477 | 20.00 | 0.0700 | 0.8777 |
| claude-sonnet-5-5 | 0.9129 | 4.00 | 0 | 0.9129 |

折后分最高的是 `claude-opus-5-5`（0.9433）。`claude-sonnet-5-5` 要赢过它，榜单分最多比它低 0.0301，实际低 0.0605，所以均衡档仍选 `claude-opus-5-5`。成本优先时候选带扩到 15 个模型，`claude-haiku-5-5`（0.7695 分，0.20 美元）的折后分 0.7695 高于 `claude-opus-5-5` 的 0.7331，选它。

同一个 `claude-sonnet-5-5`，在 `code.build` 画像里是 0.9052，在 `code` 画像里是 0.9129：画像用的指标不同，榜单分就不同，所以它在例 1 的均衡档里掉出了候选带，在这里进了带却没赢。

**例 4：第一名不可用，参照分下移**

条件和例 1 的均衡档相同，但 `claude-fable-5-1` 没有可用渠道。参照分变成 `gpt-6-astra` 的 0.9765，下限随之降到 0.9765 − 0.07 = 0.9065。`claude-sonnet-5-5`（0.9052）仍差 0.0013，在带外。带内剩 3 个模型，价格范围还是 4 到 20 美元，折扣和折后分与例 1 相同，结果仍是 `gpt-6.1-sol`。

候选带跟着“当前可用的最高分”走，不是固定按榜单名次。

**例 5：同一个 w，在不同画像里宽窄不同**

榜单分是各指标归一化值的平均，同一个 w 对不同刻度的指标意义不同。ratio 刻度（LiveBench、Artificial Analysis）里，0.07 是比最高分低 7%；elo 刻度（Design Arena，以及 LMArena 除 `lma.agent_bash` 外的各榜）里，最高分附近的 0.07 约等于 24 个 Elo 点，窗口窄得多。所以只用 elo 指标的画像带更窄，含 ratio 指标的画像带更宽。均衡档（w=0.07）下：

| 画像 | 指标刻度 | 带内模型数 | 选中 |
| --- | --- | --- | --- |
| `code.frontend` | 全是 elo | 2 | claude-sonnet-5-5 |
| `data.business` | ratio 和 elo 混合 | 17 | muse-spark-1.2 |

`code.frontend` 带内只有 `claude-opus-5-5`（0.9426，8 美元）和 `claude-sonnet-5-5`（0.8983，4 美元）。带里只有两个模型，贵的折满 0.07，便宜的折 0，折后分是 0.8726 对 0.8983，取 `claude-sonnet-5-5`：让出 0.0443 分，价格减半。

`data.business` 带内 17 个模型，下面是其中 4 个：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-opus-4-7 | 0.9970 | 10.00 | 0.0513 | 0.9457 |
| muse-spark-1.2 | 0.9807 | 2.00 | 0.0078 | 0.9729 |
| gemini-4-argon | 0.9658 | 4.00 | 0.0265 | 0.9393 |
| gemini-3.7-flash | 0.9290 | 1.50 | 0 | 0.9290 |

`gemini-3.7-flash` 是带内最便宜的（1.50 美元），但折后分 0.9290 低于 `muse-spark-1.2` 的 0.9729。最后选 `muse-spark-1.2`：比第一名低 0.0163 分，价格是五分之一。最便宜的不一定被选中。

**例 6：价格相同，折扣相同，榜单分高的中选**

jev 判成 `general.design`，均衡档。参照分是 `claude-opus-5-5` 的 1.0000，下限 0.9300，带内 9 个模型。价格最低的两个都是 0.20 美元，折扣都是 0，折后分就是榜单分：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-opus-5-5 | 1.0000 | 8.00 | 0.0561 | 0.9439 |
| claude-haiku-5-5 | 0.9563 | 0.20 | 0 | 0.9563 |
| gpt-6-luna | 0.9494 | 0.20 | 0 | 0.9494 |

另有 6 个模型未列出，折后分都低于 `claude-haiku-5-5`。它比第一名低 0.0437 分，价格是四十分之一。这个快照里 `da.slides` 停更，`general.design` 实际只剩 `lb.design` 一个指标，拿 0.0437 分换四十倍价差，依据偏薄，见第 10 节。

**例 7：从 jev 的概率到画像**

阈值取默认的 0.5，没有上一次路由，所有模型都可用：

| jev 的概率 | 画像 | 说明 |
| --- | --- | --- |
| `code.build` 0.82、`code.ops` 0.10、`general.chat` 0.08 | `code.build` | 叶子达到阈值，按叶子的榜单比较 |
| `general.chat` 0.40、`code.build` 0.35、`code.ops` 0.25 | `code` | 叶子都没到阈值，`code` 大类合计 0.60 到了，按大类的榜单比较 |
| `general.chat` 0.32、`general.knowledge` 0.30、`general.writing` 0.20、`code.build` 0.18 | 默认 | `general` 大类合计 0.82 可信，但 `general` 没有一级列表，按 `general` 画像的榜单分比较 `default_models`；仍是可信判断，会覆盖客户端的推理档位 |
| `code.build` 0.40、`data.analysis` 0.35、`general.chat` 0.25 | 沿用上一次，没有则默认 | 没有任何大类到阈值，判断不可信 |
| `unknown` 0.70、`code.build` 0.30 | 沿用上一次，没有则默认 | `unknown` 不属于任何大类，不计入大类概率；`code` 只有 0.30，判断不可信 |

**例 8：某一级没有可用模型**

一级找不到可用模型，就往下一级找，最后落到 `default_models`。每一级都按当前偏好比较，均衡档（w=0.07）下：

- `code.frontend` 的 114 个模型都不可用，只启用了 `code` 里不在 `code.frontend` 列表中的 8 个模型。画像落到 `code`，参照分是 `gpt-6-astra` 的 0.9477，下限 0.8777，带内只有它一个，结果是它。
- `general.browser` 的 26 个模型都不可用，其余模型可用。`general` 没有一级列表，直接按 `general` 画像的榜单分比较 `default_models`。可用的里最高分是 `muse-wonder`（0.8585 分，8 美元），下限 0.7885，其余都在下限之下，带内只有它一个，结果是它。

**例 9：兜底也跟偏好**

jev 失败、判断不可信或是辅助请求，又没有上一次路由可沿用时，按 `general` 画像的榜单分比较 `default_models`，用的仍是这把钥匙的偏好。所有模型都可用：

- 能力优先（w=0）：选榜单分最高的 `claude-opus-5-5`（0.9817 分，8 美元）。
- 成本优先（w=0.3）：选 `glm-5.3-flash`，候选带 25 个模型。
- 均衡（w=0.07）：下限 0.9117，带内 7 个模型，下面是其中 4 个：

| 模型 | 榜单分 | 价格 | 折扣 | 折后分 |
| --- | --- | --- | --- | --- |
| claude-opus-5-5 | 0.9817 | 8.00 | 0.0301 | 0.9516 |
| gemini-4-argon | 0.9543 | 4.00 | 0 | 0.9543 |
| gpt-6-astra | 0.9442 | 20.00 | 0.0700 | 0.8742 |
| claude-fable-5-1 | 0.9250 | 20.00 | 0.0700 | 0.8550 |

选 `gemini-4-argon`：让出 0.0274 分，价格减半。上一次路由的模型仍可用时，直接沿用，不做这次比较。

**例 10：w 调大，选中的不一定更便宜**

jev 判成 `general.search`，所有模型可用，只改 w：

| w | 选中 | 价格 |
| --- | --- | --- |
| 0.06 | claude-opus-5-5 | 8.00 |
| 0.07 | gemini-4-argon | 4.00 |
| 0.075 | claude-opus-5-5 | 8.00 |
| 0.10 | mimo-v2.6-pro | 0.54 |
| 0.115 | claude-opus-5-5 | 8.00 |
| 0.125 | mimo-v2.6-pro | 0.54 |
| 0.25 | deepseek-v4.1-flash | 0.21 |

w 从 0.07 调到 0.075，下限降低，`mimo-v2.6-pro`（0.9288 分，0.54 美元）进了候选带，成了带内最便宜的，价格下限从 4 美元降到 0.54 美元。带内所有模型的折扣都按新下限重算：`gemini-4-argon` 的折扣从 0 变成 0.0415，折后分从 0.9706 降到 0.9291，低于 `claude-opus-5-5` 的 0.9441，选中的又回到了更贵的那个。

所以调 w 以后要看日志里的 `band`，不能只看选中了谁。

**例 11：一次决策在日志里的样子**

例 1 的请求（均衡档），难度 3，`larger_model_benefit` 为 0.2，不到 0.5 所以不提档。消费日志 `admin_info.auto_router`（数值保留四位小数）：

```json
{
  "resolved_model": "gpt-6.1-sol",
  "profile": "code.build",
  "model": "gpt-6.1-sol",
  "effort": "high",
  "source": "jev",
  "decision": {
    "task_type": "code.build",
    "task_type_probability": 0.82,
    "task_type_probabilities": {"code.build": 0.82, "code.ops": 0.1, "general.chat": 0.08},
    "difficulty": 3,
    "task_change": 1,
    "larger_model_benefit": 0.2
  },
  "preference": "balanced",
  "effort_in_body": true,
  "effort_override": true,
  "pick": {
    "chosen": "gpt-6.1-sol",
    "weight": 0.07,
    "band": [
      {"model": "claude-fable-5-1", "score": 0.9865, "price": 20, "discount": 0.07, "utility": 0.9165},
      {"model": "gpt-6-astra", "score": 0.9765, "price": 20, "discount": 0.07, "utility": 0.9065},
      {"model": "claude-opus-5-5", "score": 0.97, "price": 8, "discount": 0.0301, "utility": 0.9399},
      {"model": "gpt-6.1-sol", "score": 0.941, "price": 4, "discount": 0, "utility": 0.941}
    ]
  },
  "applied_effort": "high",
  "effort_action": "set"
}
```

- `profile` 是实际用的榜单；它和 `decision.task_type` 不一致，说明发生了回退（回到大类或默认）。
- `source` 是 `jev`（本次调用）、`cache`（判断缓存命中）、`inherit`（沿用上一轮）或 `default`（没有可用信号）。
- `preference` 是这次请求用的偏好：令牌自己设的，没设则是平台的 `default_preference`。
- `pick` 只在做过质量/价格比较时出现。`weight` 是这次用的 w，`band` 是候选带，`discount` 是价格折扣，`utility` 是折后分，`chosen` 是选中的。沿用上一轮模型和按列表顺序取第一个时没有 `pick`。线上日志里的数值是完整精度，不是示例里的四位小数。
- `effort` 是难度给出的期望档位，`applied_effort` 是按模型的渠道声明收敛后真正写进请求体的档位，`effort_action` 是 `set`（写入）、`keep`（不碰客户端字段）或 `strip`（模型不支持推理，去掉客户端字段）。`effort_override` 为 true 表示这是可信的判断，覆盖了客户端的推理字段。`effort_in_body` 为 true 表示档位写进请求体、模型名保持裸名；为 false 时档位拼在模型名后缀上（如 `gpt-5-high`）。

### 7.5 选路日志

消费日志只记结果。要还原一次请求怎么走到最后那个模型，看选路日志（系统设置 → Auto 路由 → 选路日志，管理员可看）。每个 auto 请求最多一行，用请求 id 和使用日志对上；使用日志详情里的“Auto 路由”小节有“查看选路过程”按钮，没有详细日志时退回这里的消费日志摘要。

一行记的是：

- 请求长什么样：协议、人类轮数、指令和上一句的字数、有没有被截断。只记长度，不记内容。
- 判断从哪来：现调了 jev、命中判断缓存、jev 刚失败过（60 秒内不重试）、jev 调用失败、会话继承、后台辅助请求、没有可分类的指令，或者根本没有模型能服务。
- 判断可不可信，不可信的原因（jev 没答、答案没有难度、没有任何任务类型或大类的概率够高）。
- 有上一轮路由时，是否延续，以及任务变化值对阈值。
- 自适应偏好的请求：三个偏好各占的概率、期望位置、本次用的 w 和当时平台设的三档 w；没有可用判断时写明回退到均衡档。抽屉里只在这次真的做了质量/价格比较时才显示这一步，沿用上一轮模型或按榜单顺序取第一个的请求没用到 w。
- 难度怎么变成档位：难度对应的档位、是否因大模型收益提了一档、是否被上一轮的档位抬高。
- 各榜单依次试了什么，每一级的结果（质量/价格比较选出、按列表顺序取第一个、没有可用的、沿用上一轮），以及榜单里分数够高却没被选中的模型各自的原因：令牌的模型白名单不允许、分组里没启用、没有启用的渠道、没有配价格、渠道能力不满足请求、按次计费没法比价、被客户端自己的推理设置排除。每个榜单最多记 8 个，其余只计数。
- 没有任何模型能服务时，按原因汇总所有榜单里的模型个数，并各列几个例子。

这些只进选路日志，不进消费日志的 `admin_info.auto_router`，免得每条消费日志都变大。

记哪些请求由 `autorouter_setting.route_log_mode` 决定，改了立即生效：

| 值 | 记什么 |
| --- | --- |
| `off` | 不记 |
| `decisions`（默认） | 真正做了决定的请求：这次现调了 jev（无论成败），或者找不到任何模型 |
| `all` | 所有 auto 请求。命中判断缓存的后续请求、后台辅助请求、会话继承得到的结果都是在重复之前的决定，量大，只有这一档才记 |

日志写在日志库里（`LOG_SQL_DSN` 为空时与主库同库），由异步 writer 批量写入，不在请求路径上：队列写满时丢弃并计数，不会拖慢选路；写入失败只记日志。队列容量、每批条数、最长等待毫秒数、清理时每块删除的行数用 `AUTO_ROUTE_LOG_BUFFER_SIZE`（默认 4096）、`AUTO_ROUTE_LOG_BATCH_SIZE`（200）、`AUTO_ROUTE_LOG_FLUSH_MS`（2000）、`AUTO_ROUTE_LOG_CLEANUP_CHUNK`（1000）调整，范围见 `.env.example`。每行的完整过程（`detail`）不超过 32KB，超出时依次去掉比较候选带、整个路由结果。

保留期是 `autorouter_setting.route_log_retention_days`（默认 7，范围 1-365）。非 ClickHouse 日志库由系统任务 `auto_route_log_cleanup` 每小时分块删除过期行；ClickHouse 日志库靠建表 TTL（`LOG_SQL_CLICKHOUSE_TTL_DAYS`）过期，不走这个任务，也不受保留天数影响。

读取接口：`GET /api/auto-route-logs/`（分页，按请求 id、用户、模型、结果、判断来源、任务类型和时间筛选，列表不带 `detail`）和 `GET /api/auto-route-logs/:request_id`（单条，带解码后的 `detail`；没有这条日志时 `data` 为 `null`，不是错误）。都需要管理员，访问令牌需要 `log:read`。

## 8. 在线刷新

沿用系统任务 `autorouter_rank`：master 节点在数据库租约下按 `rank_refresh_interval_hours`（默认 24，最小 1）执行，依次写入 `task_models`、`task_scores`、`default_models`、`rank_sources`，其他节点经配置同步生效，不需要重启。任一来源失败或任一画像没有模型时不写配置，线上保留上一份榜单。

- **启用条件**：`autorouter_setting.enabled` 与 `rank_refresh_enabled` 为 true，且有 Artificial Analysis 的 key，以及 Design Arena 或 OpenRouter 的 key 之一。key 优先取系统设置（Auto 路由 → 基础）里存的值，没存时回退到同名环境变量，见下表。OpenRouter 模型列表是公开接口，不需要 key。
- **管理**：系统设置（Auto 路由 → 榜单）可以看当前生效的榜单、立即刷新、恢复内置榜单；jev 的地址、模型、密钥和三个榜单来源的 key 也在界面里配。`*_api_key` 类选项只写不读，选项接口不会返回它们的值，状态接口只说明每个值来自选项、环境变量还是没设。
- **网络**：网关需能访问 `openrouter.ai`、`livebench.ai`、`hf-mirror.com`、`artificialanalysis.ai`、`www.designarena.ai`。不再逐模型查询端点延迟，一次刷新通常在一分钟内完成。
- **数据新鲜度**：按 OpenRouter 目录的上架时间（`created`）取每个来源评过分的最新 10 个候选（`-stale-probe`），某项指标在这 10 个里都没有数据时视为停更，本轮不参与打分，记入 `rank_sources.stale_metrics` 和刷新记录。来源评过分的候选不足 10 个时不做判断；视觉指标只看支持图像输入的候选。
- **分类版本**：`rank_sources` 记录 `taxonomy_version`。启动或配置同步时若已存的版本与代码中的分类不一致，运行时改用内置 `task_models.json`，并立即触发一次刷新，不等周期。`task_scores` 与 `task_models` 按同一个版本判定，一起退回内置的，避免旧分类下的分数配上新分类的列表。

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `autorouter_setting.rank_refresh_enabled` | `true` | 是否定时刷新 |
| `autorouter_setting.rank_refresh_interval_hours` | `24` | 刷新周期，单位小时 |
| `autorouter_setting.task_models` | 内置榜单 | 画像名 → 模型列表 |
| `autorouter_setting.task_scores` | 内置榜单 | 画像名 → 模型 → 榜单分，第 7.3 节比较用；与 `task_models` 一起生成、一起刷新 |
| `autorouter_setting.default_models` | 内置榜单 | 默认模型列表 |
| `autorouter_setting.default_preference` | `balanced` | 令牌没设 auto 偏好时用的偏好：`capability`、`balanced`、`cost`、`adaptive`；不认识的值按 `balanced` |
| `autorouter_setting.cost_weights` | `{"capability":0,"balanced":0.07,"cost":0.3}` | 第 7.3 节三个固定偏好的 w：为了价格最多让出的榜单分；JSON 里没写的偏好用内置值，负数和 NaN 按 0，大于 1 按 1；只接受这三个键，`adaptive` 没有自己的 w，写进来会被拒绝，自适应请求从这三个 w 里折算 |
| `autorouter_setting.rank_sources` | 内置榜单 | 各来源快照日期、生成时间、`taxonomy_version` |
| `autorouter_setting.jev_endpoint`、`jev_model`、`jev_api_key` | 空 | jev 的地址、模型和密钥；为空时分别回退到环境变量 `JEV_ENDPOINT`、`JEV_MODEL`、`JEV_API_KEY`。写入前服务端校验：地址必须是 http/https，密钥和模型不能含空白或换行 |
| `autorouter_setting.artificial_analysis_api_key`、`design_arena_api_key`、`openrouter_api_key` | 空 | 三个榜单来源的 key；为空时回退到下面同名的环境变量 |
| `autorouter_setting.route_log_mode` | `decisions` | 第 7.5 节：选路日志记哪些请求，`off`、`decisions`、`all` |
| `autorouter_setting.route_log_retention_days` | `7` | 选路日志保留天数，1-365 |
| 环境变量 `ARTIFICIAL_ANALYSIS_API_KEY` | 无 | Artificial Analysis 免费接口所需；未设置时不刷新 |
| 环境变量 `DESIGN_ARENA_API_KEY` | 无 | Design Arena 接口所需（`sk_live_` 开头，经 designarena.ai/developers/apply 申请）；未设置时经 OpenRouter 读取 |
| 环境变量 `OPENROUTER_API_KEY` | 无 | 未设置 `DESIGN_ARENA_API_KEY` 时读取 OpenRouter 转发的 Design Arena 数据 |

`modelrank` 命令行同步调整：新增 `-aa-key-env`（默认 `ARTIFICIAL_ANALYSIS_API_KEY`）、`-da-key-env`（默认 `DESIGN_ARENA_API_KEY`）、`-openrouter-key-env`（默认 `OPENROUTER_API_KEY`）、`-artificial-analysis`、`-design-arena`（接口地址），去掉 `-api-key-env`；去掉 `-usage-bonus`、`-latency-tolerance`。输出文件的 `weights` 改为 `profiles`（画像名 → 指标列表），`evidence` 记录每个模型在各画像下的分数、有数据的指标数和各指标归一化值。

## 9. 代码改动

| 位置 | 改动 |
| --- | --- |
| `pkg/autorouter/jev.go` | `task_type` 选项换成 12 个二级类 + `unknown`，判定标准用第 4.1 节的文案；二级类名的前缀即一级类 |
| `pkg/autorouter/modelrank/metrics.go` | 指标目录换成第 3 节；`taskWeights` 换成画像 → 指标列表；`code`、`data` 一级画像，`general` 一级画像即默认画像；`designArenaCategories` 只保留 `website`、`uicomponent`、`slides`；删除 `usageTags`、`openRouterBenchmarks` |
| `pkg/autorouter/modelrank/sources.go` | 新增 Artificial Analysis 免费接口与 Design Arena 接口加载，无 Design Arena key 时经 OpenRouter `/benchmarks` 读取；新增指标新鲜度统计；LMArena 增加 `creative_writing` 与两个 `industry_*` 分类；删除 OpenRouter 自测 benchmark（GPQA、τ-bench）、AA 指数转发、任务用量、端点延迟的加载 |
| `pkg/autorouter/modelrank/rank.go` | 等权平均与按指标数的覆盖要求；删除用量加分；帕累托比较只看分数与价格 |
| `pkg/autorouter/policy.go` | 按第 7.2 节的顺序选画像与列表，记录 `route_profile`、`route_fallback`；每一级列表和 `default_models` 都先按第 7.3 节比较，比不出再取第一个可用的 |
| `pkg/autorouter/select.go` | 第 7.3 节的质量/价格比较：候选带、价格折扣、按折后分排名取第一，并返回比较过程写进日志；偏好常量 `capability`、`balanced`、`cost` |
| `setting/autorouter_setting/` | 新的内置 `task_models.json`；`taxonomy_version` 校验；`task_scores` 从内置 `evidence` 播种，随 `task_models` 一起按版本退回内置；`default_preference`、`cost_weights`，`Policy(preference)` 按令牌的偏好取 w |
| `model/token.go`、`model/token_cache.go`、`controller/token.go`、`middleware/auth.go` | 令牌新增 `auto_preference` 列（可空，空为平台默认），随令牌创建/更新接口读写，更新时不传则保留原值，不认识的值返回错误；Redis 令牌缓存脚本多存一个字段；鉴权后放进请求上下文 |
| `web/src/features/keys/` | 令牌表单新增“Auto 模型偏好”下拉，独立于“路由倾向”；审计日志的变更字段多一个标签 |
| `pkg/autorouter/jev.go`、`select.go`、`trace.go`、`policy.go` | 自适应偏好：jev 多问一个 `mode`，`QuestionsVersion` 升到 v6；`Decision.ModeProbabilities`；`PreferenceAdaptive` 和只含三个固定偏好的 `Preferences` 之外的 `SelectablePreferences`；`TierWeights.Blend` 按第 7.3 节折算 w；`SelectRoute` 在自适应偏好下用折算的 w 覆盖 `Policy.CostWeight`，并把过程记进 `trace.mode` |
| `setting/autorouter_setting/`、`controller/token.go` | `Policy()` 同时带出三档的 w（`Policy.Tiers`），`default_preference` 和令牌的 `auto_preference` 接受 `adaptive`，`cost_weights` 仍只接受三个固定偏好 |
| `web/src/features/system-settings/autorouter/`、`web/src/features/keys/` | 令牌表单和平台默认偏好多一项“按请求判断”（`SELECTABLE_AUTO_PREFERENCES`），成本权重编辑器仍是固定三档；选路过程抽屉多一步“成本倾向” |
| `pkg/autorouter/jevexp/` | 汇总里多一行 `mode`：各偏好被选为最可能的次数、有多少条没有可用答案、期望位置的平均值，用来在切换令牌之前看真实流量里 `mode` 的分布 |
| `controller/system_task_handlers.go` | 刷新任务的启用条件改为检查 `ARTIFICIAL_ANALYSIS_API_KEY`，以及 `DESIGN_ARENA_API_KEY` 或 `OPENROUTER_API_KEY`；刷新时同时写 `task_scores` |
| `middleware/distributor.go`、`relay/helper/price.go` | 候选按“模型 + 渠道”判定可用；`RoutingPricer` 按用户侧价格给候选报价，只读价格配置，不写 `PriceData`、不碰额度 |
| `service/autorouter.go`、`service/log_info_generate.go` | 调用 jev 前先检查有没有可用模型；比较过程写进 `admin_info.auto_router` |
| `pkg/autorouter/trace.go`、`policy.go`、`select.go` | 第 7.5 节：`SelectRoute` 多收一个可为 nil 的 `trace` 参数，记判断是否可信、是否延续、难度怎么变成档位、各榜单试了什么，以及比较里被跳过的模型和原因（只进 trace，`Pick` 里的这部分字段不导出，消费日志的摘要不变大）；`Policy.ExplainUnroutable` 按原因汇总 |
| `model/auto_route_log.go`、`service/auto_route_log.go`、`service/event_writer.go` | 选路日志表、按 `route_log_mode` 决定记不记、异步批量写入（与能力门控事件共用泛型 writer）、详情超过 32KB 时精简、过期清理（`purgeBefore`，也供能力门控用） |
| `controller/auto_route_log.go`、`controller/autorouter.go`、`controller/system_task_handlers.go`、`router/api-router.go` | 选路日志的列表和按请求 id 取单条；auto 管理接口（状态、有效榜单、立即刷新、恢复内置）；清理任务 `auto_route_log_cleanup`；访问令牌路由表登记上述接口 |
| `setting/autorouter_setting/` | 另外：jev 连接和三个榜单来源 key 改走选项（为空回退环境变量）；`route_log_mode`、`route_log_retention_days`；所有 `autorouter_setting.*` 写入前由服务端校验，避免存进去却因为解析失败永远不生效 |
| `web/src/features/system-settings/autorouter/` | 系统设置里的“Auto 路由”分类：基础、偏好与成本、判断与推理强度、榜单、选路日志五节；选路日志含记录设置、日志列表和选路过程抽屉（`route-log/`） |
| `web/src/features/usage-logs/` | 使用日志里标出 auto 请求并能按它筛选（`auto_routed` 列，老数据的空值按“不是 auto”算）；详情里的“Auto 路由”小节和“查看选路过程” |

## 10. 已知限制

- **小榜覆盖有限**：`lb.*` 只含最新版本跑过的模型（`20261025` 为 31 个），`lma.document`、`lma.search` 各四十余个；两指标画像只要有其中一项即可上榜；`general.browser` 只用 `lb.browser`，只覆盖 LiveBench 收录的模型。未收录模型落到一级列表。
- **Artificial Analysis 免费接口只有指数**：单项 benchmark（如 Terminal-Bench、FrontierCode）需要付费接口，因此 `aa.*` 只用 Intelligence 指数（v5）和 Strategy & Ops 能力指数。接口仍返回 `coding_index`、`agentic_index` 字段，但它们已不在 v5 指数体系中、不再随新模型更新，不使用。Intelligence v5 只覆盖 2026-03 之后发布的模型。
- **LMArena 分榜新旧不一**：文档、搜索榜可能比文本榜旧一个月以上，以 `rank_sources` 记录的日期为准。
- **分类准确率待校准**：12 个二级类的判定标准需要用标注集验证 jev 的选择准确率，`min_task_type_probability` 随之调整；一级类兜底保证二级类判错时仍落在相近的榜单上。
- **w 是全局的，窗口宽度随画像变**：见第 7.4 节例 5。只含 elo 指标的画像候选带窄，含 ratio 指标的宽。按 2026-10-28 的内置榜单和榜单文件里的价格，12 个二级类里，均衡档（w=0.07）有 8 个选的不是榜单第一名，平均比第一名低 0.0178 分，最多低 0.0455，价格中位数是第一名的一半；成本优先（w=0.3）有 11 个不是第一名，平均低 0.0781，最多低 0.2329（`code.build` 选了 `claude-haiku-5-5`），价格中位数是第一名的 6%；能力优先 12 个都选第一名。这些权重是按这份快照定的起步值，榜单或价格刷新后要看日志里的 `band` 重新评估。按画像分别标定还没做。
- **w 调大，选中的不一定更便宜**：见第 7.4 节例 10。候选带随 w 变宽，进带的更便宜的模型会拉低价格下限，改变带内所有模型的折扣，所以 w 的取值和选中的模型之间不是单调的。
- **折后分接近时选择不稳**：例 1 的均衡档里 `gpt-6.1-sol` 只比 `claude-opus-5-5` 高 0.0011，价格或榜单分的一点变动就会换人。这是 w 取值的自然结果，不是错误；`band` 里能看到这种近乎并列的情形。
- **偏好只管选哪个模型**：思考强度仍由难度决定，不随偏好变；Scheduler 的路由倾向也与它无关。对话中途换偏好，要到下一个新任务才生效（同任务延续沿用上一次的模型）。
- **`mode` 的线索很少**：送去判断的只有用户最近两句话，“省钱”“这是线上事故要稳”这类线索只能出自这两句话的措辞，多数请求没有，`mode` 会集中在 `balanced`，自适应偏好对普通流量的影响因此很小，值不值得开主要看三项判据怎么写。先看真实分布再把令牌切过去：选路日志默认模式 `decisions` 记下每次现调 jev 的请求，里面的 `decision.mode_probabilities` 就是 `mode` 的原始答案；有 Langfuse 导出时也可以用 `jevexp` 离线汇总。
- **`mode` 的返回格式只在测试桩上验证过**：`mode` 是 choice 题，和 `task_type` 一样应该返回各选项的概率，但真实 jev 对这道新题是否如此还没有验证。只返回 `choice` 而没有概率时，三项都按 0 读，等于没有可用答案，自适应偏好退回均衡档，不会出错。
- **自适应偏好每次都多问一题**：每次调 jev 多一个问题，延迟增加多少未测，上线后看选路日志的 `jev_latency_ms`。升到 v6 时已缓存的判断失效一次，之后每个仍活跃的（令牌，指令）多调一次 jev。
- **单指标画像的分数偏薄**：`general.browser`、`general.research` 只有一个指标；`general.design`、`general.search`、`general.writing` 在 2026-10-28 的快照里因 `da.slides`、`lma.search`、`lma.document` 停更，实际只剩一到两个有效指标。用很小的分差换很大的价差，依据比多指标画像弱。
- **比价用的是用户侧价格**：榜单文件里的 `price_per_mtok` 是 OpenRouter 的价格，只用来给 `default_models` 排序和本文示例。运行时按第 7.3 节用用户侧价格比较，两者可能差很多。
