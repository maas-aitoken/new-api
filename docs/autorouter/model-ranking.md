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

两级分类：3 个一级类（`code`、`data`、`general`）、12 个二级类，外加 `unknown`。一级类参照 OpenRouter 任务分类的 `code:*`、`data:*` 与通用标签划分。jev 只在二级类和 `unknown` 中单选；一级类不单独提问，二级类名的前缀就是它的一级类。

每个一级类和二级类都有自己的画像（指标集），一级类画像用于二级列表无可用模型时（见第 7 节）。`general` 的一级画像就是默认画像，对应 `default_models`。

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
- **不再问 `mode`（问题集版本 v7）**：v6 曾加过 `mode` 问题，让 jev 判断用户更看重质量还是成本，供自适应偏好插值权重。质量与成本的取舍应由任务难度决定，而不是另一道判断题，所以 v7 去掉了这道题和自适应偏好。问题集变了，缓存的判断随版本号失效一次。

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

`task_models` 以画像名为键，共 14 个列表（12 个二级类 + `code`、`data` 两个一级类）。每个列表包含所有满足覆盖要求的模型，按分数降序；同分时价格低的在前，再相同按 id。列表不截断。运行时在当前可用的模型里按第 7.3 节比较质量、成本、容量和成功率，比不出结果时取第一个可用的模型。

`default_models` 按 `general` 一级画像生成，同时充当 `general` 的一级列表：

1. 先放分数 ≥ 最高分 0.9 倍、且在分数/价格帕累托前沿上的模型，按价格升序；
2. 再放其余有 `general` 分数的模型，按分数降序。

运行时 `default_models` 也用 `general` 画像的榜单分，按第 7.3 节比较质量、成本、容量和成功率；上面的顺序只在比较选不出结果时才起作用。

### 7.2 选择顺序

jev 返回各二级类的概率，并选出概率最高的一个，记为 `leaf`，其一级类为 `parent`：

| 条件 | 画像 |
| --- | --- |
| `leaf` 是二级类 | `leaf` |
| `leaf` 是 `unknown` | 沿用上一次路由；没有上一次时用 `default_models` |
| 没有合法判断 | 同 `unknown` |

确定画像后按 `task_models[leaf] → task_models[parent] → default_models` 依次取模型。`general` 没有一级列表，这一步即 `default_models`。每一级 `task_models` 列表先按第 7.3 节比较质量、成本、容量和成功率；列表里没有带榜单分且可比价的可用模型时，取该列表第一个可用的模型；仍然没有才进入下一级。`default_models` 用 `general` 画像的榜单分，同样按第 7.3 节比较；比不出结果时取列表里第一个可用的模型。

jev 的回答分三种：

- **合法判断**：答出了难度（大于 0），任务类型是给定的 12 个二级类或 `unknown` 之一。现调 jev 和判断缓存命中都一样。`unknown` 是合法的答案，意思是这条消息离开前文说不出要做什么。
- **不合法判断**：jev 答了，但没有难度，或者任务类型不在给定的选项里。
- **没有判断**：jev 调用失败、超时，或 60 秒内刚失败过、不再重试。辅助请求和读不出指令的请求不调 jev，也没有判断。

`leaf` 是二级类时，按上表的顺序选模型，思考强度由这次判断定下，按模型的渠道声明收敛后写进请求体，覆盖客户端写的推理字段；模型没有推理声明时不动客户端的字段。`leaf` 是 `unknown` 时没有榜单可用，沿用上一次路由，没有上一次时用 `default_models` 和 `default_effort`，不碰客户端的推理字段；不合法判断和没有判断时也这样做，原因记进选路日志。没有可读文字的子 agent 沿用所在会话最近一条人类指令的路由；那条路由的档位由判断定下时，子 agent 同样覆盖推理字段。

`leaf` 是 `unknown`，但 `larger_model_benefit` 达到 `upgrade_benefit` 时，模型照上面的办法定：上一次的模型仍可用就沿用，没有才比较 `default_models`；思考强度则由这次判断按下面的规则定，和 `leaf` 是二级类时一样写进请求体、覆盖客户端的推理字段。这类消息多是对上一轮结果不满、要求重做（“还是不行”“不对，重来”），任务类型说不出来，但需要更强的推理。判断模型时撇开客户端的推理字段，`source` 记 `jev` 或 `cache`。

思考强度按顺序定：

1. 按难度从 `difficulty_efforts` 取档位，难度超出档位表的用最后一档。
2. 沿用上一次的模型时（同任务延续，即 `task_change < continuation_task_change`；或上面的 `unknown` 情形），上一次的档位更高就取上一次的。同任务延续只延续出自正确分类的模型：上一次 jev 失败、判断不合法或任务类型是 `unknown`，模型是兜底来的（且不是从更早一次正确分类沿用下来的），本次分类出二级类时不看 `task_change`，按本次的榜单重新选；本次仍没有分类时照旧沿用。
3. `larger_model_benefit` ≥ `upgrade_benefit` 时提升一档，到 `max` 为止：沿用上一次的模型时比上一次的档位高一档，第 2 步的档位已经到了就不再加；没有沿用时比第 1 步的档位高一档。

提升按上一次算：本轮难度给的档位已经比上一次高，就算已经提升过了，不再多加一档。用户连续几轮都不满意时，每一轮都比上一轮高一档。`ResolveVariant` 解析思考强度变体时，档位不在档位表里也探测 `-medium` 后缀。“可用”的含义见第 7.3 节。

路由日志与 trace 记录 `task_type`（jev 选中的二级类）、`route_profile`（实际使用的画像）和 `route_fallback`（`leaf` / `class` / `default`），用于复核分类和兜底的发生频率。消费日志的 `admin_info.auto_router` 带完整的决策过程，包括第 7.3 节的比较明细，成功和失败的请求都写，样例见第 7.4 节。一次请求是怎么一步步走到最后那个模型的，另存在选路日志里，见第 7.5 节。

### 7.3 统一效用评分

每一级 `task_models` 列表和 `default_models` 都对所有可服务、有榜单分且可按 token 计价的模型做一次整体比较。auto 只评估模型状态，不维护渠道级质量或成功率榜单；渠道能力与调度预览负责硬可用性过滤，渠道费用倍率进入比较成本。

**偏好**：三档偏好共用一套效用公式。质量强度决定榜单分的尺度，成本强度映射为固定的成本容忍度，容量和成功率强度决定运行状态惩罚。温度字段保留用于兼容旧设置和旧日志，不参与新选路。

| 偏好 | 质量强度 | 成本强度 | 成本容忍度 B（美元/百万 token） | 容量强度 | 成功率强度 |
| --- | --- | --- | --- | --- | --- |
| `capability` 能力优先 | 0.6 | 0 | 40 | 0.2 | 0.2 |
| `balanced` 均衡 | 0.4 | 0.2 | 8.5 | 0.2 | 0.2 |
| `cost` 成本优先 | 0.2 | 0.4 | 0.5 | 0.2 | 0.2 |

令牌没设偏好（或值不认识）时用平台的 `default_preference`，出厂是 `balanced`；它也不认识时用 `balanced`。这个偏好只决定 auto 选哪个模型，和 Scheduler 的路由倾向互不相干。比价时会看候选会落到哪个渠道，Scheduler 只负责给比较成本提供渠道费用倍率。

**候选**：列表里同时满足下面三条的模型。

- 在该画像下有榜单分（`task_scores`）。
- 当前可用：令牌白名单允许、所在分组启用，并且至少有一个渠道可用。判断时撇开客户端自己写的推理档位，因为档位由本次判断定下。
- 有可比较的价格：按 token 计价，且用户侧价格已配置。

没配置价格的模型计费时会直接失败，任何路径都不选。按次计价的模型可以被选中，但没法和按 token 计价的模型比较，只会在比较选不出结果时按列表顺序出现。

**步骤**：

1. 先按当前请求的能力、白名单、分组和价格过滤候选，再估算首次尝试的比较成本。Scheduler 预览明确没有渠道的模型直接排除。不按质量分差预先截断候选。
2. 对每个候选计算统一效用分。质量、成本、容量和成功率始终在同一公式中发挥作用。
3. 按效用分降序排列，选第一名。效用相同先选比较成本较低者，再按模型名字稳定排序。没有随机抽样，候选数量不会稀释高分模型的选择机会。

设 `Q` 为榜单分，`C` 为比较成本，`A` 为剩余容量，`S` 为平滑成功率，`B` 为当前偏好的成本容忍度：

```text
QualityScale = normalized_quality_strength / (0.6 - min(normalized_cost_strength, 0.4))
CostPenalty = (C / B)^2 + 0.02 × log2(1 + C)
CapacityPenalty = (0.04 × ln(2) / 0.25) × normalized_capacity_strength / 0.2 × (1 - A)
SuccessPenalty = (0.04 × ln(2) / 0.10) × normalized_success_strength / 0.2 × (1 - S)
Utility = QualityScale × Q - CostPenalty - CapacityPenalty - SuccessPenalty
```

`B` 由成本强度在三个内置锚点之间按对数插值，归一化成本强度达到 0.4 时取下限 0.5。比较成本为负，或成本惩罚无法表示为有限数时，该模型不参与评分比较。同等质量和运行状态下成本越低，效用越高。质量接近时，较贵模型需要足够的质量增益来覆盖额外成本。加入更便宜的模型时，其他模型的成本惩罚保持原值。

**容量**：直接读取模型-渠道能力设置的 RPM、TPM、并发上限和当前用量。渠道选择沿用首次尝试的实际规则：调度器预览确定渠道时只计算该渠道，否则按会话亲和或渠道权重计算首次命中概率；已禁用、被能力门控排除的渠道不参与。相同容量池只计算一次；非资源池渠道只有至少设置一项上限时才参与。

对每个候选模型，按渠道命中概率 `p` 聚合有效上限 `L` 和当前用量 `U`。对存在上限的维度计算 `R = clamp((L - U) / L, 0, 1)`，`Capacity` 取 RPM、TPM、并发三个 `R` 的最小值。没有任何有效上限时容量为 1；资源池耗尽时容量为 0。当前用量沿用模型-渠道能力页的 60 秒 RPM/TPM 窗口和在途并发口径。

**成功率**：使用同一 5 分钟窗口的模型级真实结果和低样本先验：

```text
Success_i = (Successes + 4) / (Samples + 4)
```

客户端取消、参数错误、上下文超限和额度耗尽不计样本；超时、上游错误、异常截断等计失败，正常完成计成功。该统计独立于尝试日志开关和采样率。跨实例使用 Redis，Redis 不可用或读取失败时使用本节点内存窗口。

**参数**：`autorouter_setting.preference_weights`（偏好 → 完整策略）和 `default_preference`（默认 `balanced`）。三档默认原始强度见上表。改动即时生效。JSON 里没写的偏好或维度用内置值，直接写库的负数和 NaN 按 0，大于 1 按 1；设置接口会把五个存储值都限制在 0 到 1。所有画像共用同一套偏好策略；按令牌设的只有偏好这一档。

**会落到哪个渠道**：

- Scheduler 决定渠道（强制模式，或金丝雀命中）时，问 Scheduler 的只读预览 `POST /v1/preview`：一次问所有可服务候选，各自现在会被调度到哪个渠道。预览与正式调度共用同一段排序，同一份评分、额度和用量下选的是同一个渠道；预览不写决策审计、不计调度指标。一次最多问 64 个模型，多出的按原生规则估算。Scheduler 地址按顺序问，回 404 的地址（版本太旧，或前面的网关只放行 `/v1/schedule`）一分钟内跳过、接着问下一个。预览不可用、超时（整次 `SchedulerShadowTimeoutMS`，默认 100ms）、所有地址都回 404，或某个模型还没被 Scheduler 加载过时，这个模型按下面的原生规则估算；真正问过却没拿到回答时记一条 WARN 日志，原因同时写进选路日志（见 7.5 节的 `pick.preview`）。
- 其余情况按原生选渠道的规则：会话亲和绑定了渠道就是那个渠道；否则按分组顺序取第一个过滤后还有渠道的分组，在最高优先级那一档里按权重算各渠道的命中概率，倍率取期望。
- 渠道范围与选定模型后一致：令牌分组里可用的渠道，能力门控 enforce 下再按同一口径收窄。计费分组取首次会命中的分组（令牌分组是 `auto` 时，是第一个在这个渠道上服务该模型的分组），用户价按这个分组的倍率算。

**费用倍率**：在模型管理 → 能力设置的渠道卡上配，按用户请求的模型名（映射之前）存，和计费同侧：同一渠道里映射到同一上游的两个别名各配各的倍率。RPM/TPM/并发额度则按映射后发给上游的名字存，几个别名共用。没配倍率按 1。费用倍率不参与计费，只乘 `cost_actual` 和这里的比较成本；统一调高候选的倍率会增大绝对成本惩罚，也可能改变模型排序。

**价格**：`price` 是用户侧混合报价，只进入 `cost = price × 渠道费用倍率` 和统一效用公式的 `CostPenalty`。费用倍率不改变真实计费；容量和成功率通过各自的惩罚项参与效用。

**不做比较的情况**：

- 同任务延续，沿用上一次的模型。所以对话中途换偏好，要到下一个新任务才生效。
- 辅助请求、jev 失败或任务类型是 `unknown` 时，上一次的模型仍可用就沿用它；没有可沿用的，才按 `general` 画像的榜单分比较 `default_models`。
- 该级列表没有带榜单分的可用模型，或者没有可比价的候选，取列表第一个可用的。

调用 jev 之前，网关先扫一遍 `default_models` 和各 `task_models`：一个可用模型都没有就直接返回 503，不花这次 jev 调用；候选是被能力门控全部淘汰的，错误信息里会列出未满足的能力维度。

### 7.4 示例

下面的数字只用来演示统一效用公式，不对应某份榜单快照。设均衡档质量强度为 0.4、成本强度为 0.2、容量和成功率强度均为 0.2，因此质量尺度为 1，成本容忍度 B 为 8.5。

**例 1：同一组候选的 Utility**

三个候选都可服务、可计价、有榜单分；比较成本已经乘过渠道费用倍率。容量和成功率使用现有定义。

| 模型 | Quality | 成本 | Capacity | Success |
| --- | --- | --- | --- | --- |
| strong | 0.98 | 10.00 | 0.70 | 100/104 |
| cheap | 0.91 | 1.00 | 0.50 | 8/12 |
| idle | 0.94 | 3.16 | 0.80 | 1 |

`strong` 的成本惩罚为 `1.4533`，容量惩罚为 `0.0333`，成功率惩罚为 `0.0107`，最终效用为 `-0.5172`。按完整精度计算，三个模型的效用降序为 `idle`（0.7385）、`cheap`（0.7283）、`strong`（-0.5172），选择 `idle`。

**例 2：偏好改变成本容忍度**

同样三个候选，能力优先的 B 为 40，均衡的 B 为 8.5，成本优先的 B 为 0.5。成本容忍度越低，价格惩罚越大，但质量、容量和成功率仍在同一个公式里计算。能力优先不会无条件选最高质量模型，质量差不足以覆盖价差时仍会选中等模型。

**例 3：近期流量分散请求**

窗口内 `busy` 的 Capacity 为 0.1，`idle` 的 Capacity 为 0.9。两者其它输入相同，`idle` 少承担 `0.1109 × (0.9 - 0.1) ≈ 0.0887` 的容量惩罚，因此效用更高。下一次选路读取新的容量和成功率后，首选模型可以确定性换位，不需要随机抽样或失败重试来分散流量。

**例 4：从 jev 的概率到画像**

没有上一次路由，所有模型都可用：

| jev 的概率 | 画像 | 说明 |
| --- | --- | --- |
| `code.build` 0.82、`code.ops` 0.10、`general.chat` 0.08 | `code.build` | 按概率最高的叶子的榜单比较 |
| `general.chat` 0.40、`code.build` 0.35、`code.ops` 0.25 | `general.chat` | 概率最高的叶子是 `general.chat`，按它的榜单比较 |
| `unknown` 0.40、`code.build` 0.30、`code.ops` 0.30 | 沿用上一次，没有则默认 | 概率最高的是 `unknown`，没有榜单可用；`larger_model_benefit` 不到 `upgrade_benefit` 时不碰客户端的推理档位 |

**例 5：一次决策在日志里的样子**

例 1 的请求（均衡档），难度 3，`larger_model_benefit` 为 0.2，不到 0.5 所以不提档。效用最高的 `idle` 被选中。消费日志 `admin_info.auto_router`（数值保留四位小数）：

```json
{
  "resolved_model": "idle",
  "profile": "code.build",
  "model": "idle",
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
    "chosen": "idle",
    "dimensions": {"quality": 0.4, "cost": 0.2, "capacity": 0.2, "success": 0.2, "temperature": 0},
    "quality_scale": 1,
    "cost_scale": 8.5,
    "capacity_penalty": 0.1109,
    "success_penalty": 0.2773,
    "band": [
      {"model": "idle", "score": 0.94, "price": 3.16, "cost_penalty": 0.1793, "capacity_penalty": 0.0222, "capacity": 0.8, "success": 1, "samples": 0, "utility": 0.7385},
      {"model": "cheap", "score": 0.91, "price": 1, "cost_penalty": 0.0338, "capacity_penalty": 0.0555, "success_penalty": 0.0924, "capacity": 0.5, "success": 0.6667, "samples": 8, "utility": 0.7283},
      {"model": "strong", "score": 0.98, "price": 10, "cost_penalty": 1.4533, "capacity_penalty": 0.0333, "success_penalty": 0.0107, "capacity": 0.7, "success": 0.9615, "samples": 100, "utility": -0.5172}
    ]
  },
  "applied_effort": "high",
  "effort_action": "set"
}
```

- `profile` 是实际用的榜单；它和 `decision.task_type` 不一致，说明发生了回退（回到大类或默认）。
- `source` 是 `jev`（本次调用）、`cache`（判断缓存命中）、`inherit`（沿用上一轮）或 `default`（没有可用信号）。
- `preference` 是这次请求用的偏好：令牌自己设的，没设则是平台的 `default_preference`。
- `pick` 只在做过比较时出现。`dimensions` 记录归一化后的策略强度，`quality_scale`、`cost_scale`、`capacity_penalty` 和 `success_penalty` 记录本次公式参数，`band` 保留历史字段名但按效用分降序排列，`utility` 是确定性选路分，`chosen` 是第一名。旧日志里的 `probability` 只作为历史字段保留。沿用上一轮模型和按列表顺序取第一个时没有 `pick`。线上日志里的数值是完整精度，不是示例里的四位小数。
- `band` 的每一项还有 `cost_multiplier`（首次会落到的渠道的费用倍率）、`cost`（比较成本 = `price` × 倍率）、`cost_penalty`、`capacity_penalty`、`success_penalty`、`capacity`、`success`、`samples` 和 `utility`。旧日志可能只有 `cost_score` 和 `probability`。`cost_source` 为 `scheduler` 调度器预览、`affinity` 会话亲和或 `expected` 按命中概率的期望；`channel_id` 是确定会落到的渠道，`channels` 是参与期望的渠道个数。`price` 仍是用户侧价格。`channel_id` 来自预览时，和使用日志里实际落到的渠道对照，就能看出预览和正式调度有没有选得不一样。
- 配了 Scheduler 时，`pick.preview` 记这次比价问预览的结果：`status` 为 `answered` 表示 Scheduler 回答了；其余是没拿到回答的原因，`not_scheduler_decided`（这次不由 Scheduler 定渠道：影子模式、金丝雀没命中或紧急停用）、`not_configured`、`circuit_open`、`unsupported`（地址都回 404）、`timeout`、`failed`（带 `http_status`，连不上时没有）、`bad_response`。回答了、某个模型却没用上它的答案时，`band` 里那一项的 `preview` 写原因：`not_cached`（还没加载）、`error`、`unusable_channel`（给的渠道在 new-api 这边没法报价）、`over_limit`（超过 64 个）。选路过程里，整次的原因写在候选表上方，单个模型的原因写在它的“费用倍率”下面。
- `effort` 是第 7.2 节定出的期望档位，`applied_effort` 是按模型的渠道声明收敛后真正写进请求体的档位，`effort_action` 是 `set`（写入）、`keep`（不碰客户端字段）或 `strip`（模型不支持推理，去掉客户端字段）。`effort_override` 为 true 表示档位由这次判断定下（`leaf` 是二级类，或 `unknown` 但收益达到提档阈值），覆盖了客户端的推理字段。`effort_in_body` 为 true 表示档位写进请求体、模型名保持裸名；为 false 时档位拼在模型名后缀上（如 `gpt-5-high`）。

### 7.5 选路日志

消费日志只记结果。要还原一次请求怎么走到最后那个模型，看选路日志（系统设置 → Auto 路由 → 选路日志，管理员可看）。每个 auto 请求最多一行，用请求 id 和使用日志对上；使用日志详情里的“Auto 路由”小节有“查看选路过程”按钮，没有详细日志时退回这里的消费日志摘要。

一行记的是：

- 请求长什么样：协议、人类轮数、指令和上一句的字数、有没有被截断。只记长度，不记内容。
- 判断从哪来：现调了 jev、命中判断缓存、jev 刚失败过（60 秒内不重试）、jev 调用失败、会话继承、后台辅助请求、没有可分类的指令，或者根本没有模型能服务。
- 判断合不合法：合法时按哪些榜单选（`unknown` 没有榜单）；不合法或没有判断时的原因（jev 没答、答案没有难度、任务类型不在给定选项里）。
- 有上一轮路由时，是否延续，以及任务变化值对阈值。
- 难度怎么变成档位：难度对应的档位、是否被上一轮的档位抬高、是否因大模型收益再提了一档。
- 各榜单依次试了什么，每一级的结果（四维比较选出、按列表顺序取第一个、没有可用的、沿用上一轮），以及榜单里分数够高却没被选中的模型各自的原因：令牌的模型白名单不允许、分组里没启用、没有启用的渠道、没有配价格、渠道能力不满足请求、按次计费没法比价、被客户端自己的推理设置排除、Scheduler 此刻没有能接它的渠道（强制模式下预览给出的）。每个榜单最多记 8 个，其余只计数。
- 没有任何模型能服务时，按原因汇总所有榜单里的模型个数，并各列几个例子。

这些只进选路日志，不进消费日志的 `admin_info.auto_router`，免得每条消费日志都变大。

记哪些请求由 `autorouter_setting.route_log_mode` 决定，改了立即生效：

| 值 | 记什么 |
| --- | --- |
| `off` | 不记 |
| `decisions`（默认） | 真正做了决定的请求：这次现调了 jev（无论成败），或者找不到任何模型 |
| `all` | 所有 auto 请求。命中判断缓存的后续请求、后台辅助请求、会话继承得到的结果都是在重复之前的决定，量大，只有这一档才记 |

日志写在日志库里（`LOG_SQL_DSN` 为空时与主库同库），由异步 writer 批量写入，不在请求路径上：队列写满时丢弃并计数，不会拖慢选路；写入失败只记日志。队列容量、每批条数、最长等待毫秒数、清理时每块删除的行数用 `AUTO_ROUTE_LOG_BUFFER_SIZE`（默认 4096）、`AUTO_ROUTE_LOG_BATCH_SIZE`（200）、`AUTO_ROUTE_LOG_FLUSH_MS`（2000）、`AUTO_ROUTE_LOG_CLEANUP_CHUNK`（1000）调整，范围见 `.env.example`。每行的完整过程（`detail`）不超过 32KB，超出时依次去掉比较候选带、整个路由结果。

保留期是 `autorouter_setting.route_log_retention_days`（默认 7，范围 1-365）。非 ClickHouse 日志库由系统任务 `auto_route_log_cleanup` 每小时分块删除过期行；ClickHouse 日志库通过表 TTL 控制过期，应用建表和启动迁移不会创建、修改或删除 TTL，数据库优化与代码发版可独立进行；这里不走这个清理任务，也不受保留天数影响。

读取接口：`GET /api/auto-route-logs/`（分页，按请求 id、用户、模型、结果、判断来源、任务类型和时间筛选，列表不带 `detail`）和 `GET /api/auto-route-logs/:request_id`（单条，带解码后的 `detail`；没有这条日志时 `data` 为 `null`，不是错误）。都需要管理员，访问令牌需要 `log:read`。

## 8. 在线刷新

沿用系统任务 `autorouter_rank`：master 节点在数据库租约下按 `rank_refresh_interval_hours`（默认 24，最小 1）执行，依次写入 `task_models`、`task_scores`、`default_models`、`rank_sources`，其他节点经配置同步生效，不需要重启。任一来源失败或任一画像没有模型时不写配置，线上保留上一份榜单。

- **启用条件**：`autorouter_setting.enabled` 与 `rank_refresh_enabled` 为 true，且有 Artificial Analysis 的 key，以及 Design Arena 或 OpenRouter 的 key 之一。key 优先取系统设置（Auto 路由 → 基础）里存的值，没存时回退到同名环境变量，见下表。OpenRouter 模型列表是公开接口，不需要 key。
- **管理**：系统设置（Auto 路由 → 榜单）可以看当前生效的榜单、手工编辑（第 8.1 节）、立即刷新、恢复内置榜单；jev 的地址、模型、密钥和三个榜单来源的 key 也在界面里配。`*_api_key` 类选项只写不读，选项接口不会返回它们的值，状态接口只说明每个值来自选项、环境变量还是没设。
- **网络**：网关需能访问 `openrouter.ai`、`livebench.ai`、`hf-mirror.com`、`artificialanalysis.ai`、`www.designarena.ai`。不再逐模型查询端点延迟，一次刷新通常在一分钟内完成。
- **数据新鲜度**：按 OpenRouter 目录的上架时间（`created`）取每个来源评过分的最新 10 个候选（`-stale-probe`），某项指标在这 10 个里都没有数据时视为停更，本轮不参与打分，记入 `rank_sources.stale_metrics` 和刷新记录。来源评过分的候选不足 10 个时不做判断；视觉指标只看支持图像输入的候选。
- **分类版本**：`rank_sources` 记录 `taxonomy_version`。启动或配置同步时若已存的版本与代码中的分类不一致，运行时改用内置 `task_models.json`，并立即触发一次刷新，不等周期。`task_scores` 与 `task_models` 按同一个版本判定，一起退回内置的，避免旧分类下的分数配上新分类的列表。

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `autorouter_setting.rank_refresh_enabled` | `true` | 是否定时刷新；保存手工编辑的榜单时自动设为 `false` |
| `autorouter_setting.rank_refresh_interval_hours` | `24` | 刷新周期，单位小时 |
| `autorouter_setting.task_models` | 内置榜单 | 画像名 → 模型列表 |
| `autorouter_setting.task_scores` | 内置榜单 | 画像名 → 模型 → 榜单分，第 7.3 节比较用；与 `task_models` 一起生成、一起刷新 |
| `autorouter_setting.default_models` | 内置榜单 | 默认模型列表 |
| `autorouter_setting.default_preference` | `balanced` | 令牌没设 auto 偏好时用的偏好：`capability`、`balanced`、`cost`；不认识的值（包括已经下线的 `adaptive`）按 `balanced` |
| `autorouter_setting.preference_weights` | 第 7.3 节三档默认强度 | 第 7.3 节三个固定偏好的效用公式参数；保存原始四维强度和兼容字段温度，选路时归一化四维强度并映射成本容忍度；JSON 里没写的偏好和维度用内置值，四维全零回退该档默认强度；直接写库的负数和 NaN 按 0，大于 1 按 1；只接受这三个键 |
| `autorouter_setting.rank_sources` | 内置榜单 | 各来源快照日期、生成时间、`taxonomy_version`；手工编辑保存后多一项 `edited_at`，刷新和恢复内置会连同改动一起换掉 |
| `autorouter_setting.jev_endpoint`、`jev_model`、`jev_api_key` | 空 | jev 的地址、模型和密钥；为空时分别回退到环境变量 `JEV_ENDPOINT`、`JEV_MODEL`、`JEV_API_KEY`。写入前服务端校验：地址必须是 http/https，密钥和模型不能含空白或换行 |
| `autorouter_setting.artificial_analysis_api_key`、`design_arena_api_key`、`openrouter_api_key` | 空 | 三个榜单来源的 key；为空时回退到下面同名的环境变量 |
| `autorouter_setting.route_log_mode` | `decisions` | 第 7.5 节：选路日志记哪些请求，`off`、`decisions`、`all` |
| `autorouter_setting.route_log_retention_days` | `7` | 选路日志保留天数，1-365 |
| 环境变量 `ARTIFICIAL_ANALYSIS_API_KEY` | 无 | Artificial Analysis 免费接口所需；未设置时不刷新 |
| 环境变量 `DESIGN_ARENA_API_KEY` | 无 | Design Arena 接口所需（`sk_live_` 开头，经 designarena.ai/developers/apply 申请）；未设置时经 OpenRouter 读取 |
| 环境变量 `OPENROUTER_API_KEY` | 无 | 未设置 `DESIGN_ARENA_API_KEY` 时读取 OpenRouter 转发的 Design Arena 数据 |

`modelrank` 命令行同步调整：新增 `-aa-key-env`（默认 `ARTIFICIAL_ANALYSIS_API_KEY`）、`-da-key-env`（默认 `DESIGN_ARENA_API_KEY`）、`-openrouter-key-env`（默认 `OPENROUTER_API_KEY`）、`-artificial-analysis`、`-design-arena`（接口地址），去掉 `-api-key-env`；去掉 `-usage-bonus`、`-latency-tolerance`。输出文件的 `weights` 改为 `profiles`（画像名 → 指标列表），`evidence` 记录每个模型在各画像下的分数、有数据的指标数和各指标归一化值。

### 8.1 手工编辑

管理员可以在系统设置（Auto 路由 → 榜单 → 显示榜单 → 编辑榜单）里直接改榜单：改分、把模型从某个画像里移除、调整没有分数的模型的顺序，以及把一个模型（包括只有网关上才有的私有模型）一次加进多个画像。保存时 `PUT /api/autorouter/ranking` 把整份榜单写回 `task_models`、`task_scores`、`default_models`、`rank_sources` 四项，同一个事务里把 `rank_refresh_enabled` 设为 `false`，免得下一次定时刷新把改动整个覆盖。之后点“立即刷新”或“恢复内置榜单”仍会替换手工改动，界面在这两步之前都会确认；恢复内置不会重新打开定时刷新。

- **校验**：画像必须正好是 12 个二级类加 `code`、`data`、`general`。`general` 的列表写进 `default_models` 和 `task_scores.general`，不写进 `task_models`（写进去会多出一份与 `default_models` 重复的一级列表）。`general` 不能清空，它是最后一层兜底；其它画像可以清空，清空后退到一级类的列表或 `default_models`。模型名去掉首尾空白，同一画像里不能重复，不能是 `auto`，不能含换行或控制字符；分数为空（只当兜底）或 0 到 1。库里存的是旧分类下的榜单时，编辑页看到和改的是运行时在用的内置那份，保存后直接生效。
- **冲突与刷新**：保存要带进入编辑时看到的 `rank_sources.generated_at` 和 `edited_at`，与当前榜单对不上（期间被刷新、恢复内置或别人保存过）就拒绝，要重新加载再改；刷新任务进行中、或内容与当前榜单完全一样时也拒绝。审计动作 `autorouter.rank_edit` 记下改了哪些画像，以及新增、移除、改分的模型数。
- **分数和顺序**：有分数的模型按第 7.3 节比较质量、成本、容量和成功率，列表顺序只在比不出结果时才起作用（第 7.2 节）。所以编辑页里有分数的模型自动按分数从高到低排（同分保持原顺序），没有分数的只当兜底、排在后面，顺序由管理员定。改过 `general` 之后，它的兜底顺序从“帕累托前沿按价格升序”（第 7.1 节）变成按分数排；没改过的画像原样写回。
- **私有模型**：要让 auto 选到一个榜单外的模型，它必须进列表并且有分数，只进列表不给分只能当兜底。分数按画像归一，尺度差很多（按 2026-10-28 的内置榜单，0.90 在 `code.frontend` 排第 2/114，在 `data.business` 排第 24/146），所以添加对话框让管理员选一个榜单里的参照模型，按“参照分 + 偏移”填各画像的分数，并显示会排第几、比网关上能用的最高分低多少。这个估算只看渠道启用与否，不看价格、令牌白名单和分组。**私有模型要先在网关定价**：没定价时自用模式按默认倍率报价（约 $75/M），手工给的分再高，均衡档和成本优先也可能因为价格选更便宜的模型。
- **网关有没有**：`GET /api/autorouter/ranking` 的每个模型带 `served`：网关上有启用的渠道服务它（裸名或任一档位后缀名），判定与选路的 `ModelAvailable` 相同。编辑页用它标记模型，并可以只看网关有的模型。

## 9. 代码改动

| 位置 | 改动 |
| --- | --- |
| `pkg/autorouter/jev.go` | `task_type` 选项换成 12 个二级类 + `unknown`，判定标准用第 4.1 节的文案；二级类名的前缀即一级类 |
| `pkg/autorouter/modelrank/metrics.go` | 指标目录换成第 3 节；`taskWeights` 换成画像 → 指标列表；`code`、`data` 一级画像，`general` 一级画像即默认画像；`designArenaCategories` 只保留 `website`、`uicomponent`、`slides`；删除 `usageTags`、`openRouterBenchmarks` |
| `pkg/autorouter/modelrank/sources.go` | 新增 Artificial Analysis 免费接口与 Design Arena 接口加载，无 Design Arena key 时经 OpenRouter `/benchmarks` 读取；新增指标新鲜度统计；LMArena 增加 `creative_writing` 与两个 `industry_*` 分类；删除 OpenRouter 自测 benchmark（GPQA、τ-bench）、AA 指数转发、任务用量、端点延迟的加载 |
| `pkg/autorouter/modelrank/rank.go` | 等权平均与按指标数的覆盖要求；删除用量加分；帕累托比较只看分数与价格 |
| `pkg/autorouter/policy.go` | 按第 7.2 节的顺序选画像与列表，记录 `route_profile`、`route_fallback`；每一级列表和 `default_models` 都先按第 7.3 节比较，比不出再取第一个可用的 |
| `pkg/autorouter/select.go`、`capacity.go` | 第 7.3 节的统一效用评分：质量尺度、固定成本容忍度、渠道能力剩余容量和低样本成功率；按效用降序确定选中模型并写进日志；偏好常量 `capability`、`balanced`、`cost` |
| `setting/autorouter_setting/` | 新的内置 `task_models.json`；`taxonomy_version` 校验；`task_scores` 从内置 `evidence` 播种，随 `task_models` 一起按版本退回内置；`default_preference`、`preference_weights`，`Policy(preference)` 按令牌的偏好取完整策略 |
| `model/token.go`、`model/token_cache.go`、`controller/token.go`、`middleware/auth.go` | 令牌新增 `auto_preference` 列（可空，空为平台默认），随令牌创建/更新接口读写，更新时不传则保留原值，不认识的值返回错误；Redis 令牌缓存脚本多存一个字段；鉴权后放进请求上下文 |
| `web/src/features/keys/` | 令牌表单新增“Auto 模型偏好”下拉，独立于“路由倾向”；审计日志的变更字段多一个标签 |
| `pkg/autorouter/jev.go`、`select.go`、`trace.go`、`policy.go`、`jevexp/` | 去掉自适应偏好：jev 不再问 `mode`，`QuestionsVersion` 升到 v7；删除 `Decision.ModeProbabilities`、`PreferenceAdaptive`、`SelectablePreferences`、`PreferenceWeights.Blend`、`Policy.Tiers` 和 `trace.mode`；`jevexp` 汇总不再统计 `mode` |
| `setting/autorouter_setting/`、`controller/token.go` | `default_preference` 和令牌的 `auto_preference` 只接受三个固定偏好，写 `adaptive` 返回错误；库里已存的 `adaptive` 不迁移，运行时按不认识的值处理：令牌退到平台默认，平台默认退到 `balanced` |
| `web/src/features/system-settings/autorouter/`、`web/src/features/keys/` | 令牌表单和平台默认偏好去掉“按请求判断”；已存为 `adaptive` 的令牌显示为“跟随平台设置”，平台默认显示为“均衡”。选路过程抽屉仍能显示旧日志里的自适应策略 |
| `controller/system_task_handlers.go` | 刷新任务的启用条件改为检查 `ARTIFICIAL_ANALYSIS_API_KEY`，以及 `DESIGN_ARENA_API_KEY` 或 `OPENROUTER_API_KEY`；刷新时同时写 `task_scores` |
| `middleware/distributor.go`、`relay/helper/price.go` | 候选按“模型 + 渠道”判定可用；`RoutingPricer` 按用户侧价格给候选报价，只读价格配置，不写 `PriceData`、不碰额度 |
| `service/autorouter.go`、`service/log_info_generate.go` | 调用 jev 前先检查有没有可用模型；比较过程写进 `admin_info.auto_router` |
| `pkg/autorouter/trace.go`、`policy.go`、`select.go` | 第 7.5 节：`SelectRoute` 多收一个可为 nil 的 `trace` 参数，记判断是否合法、按哪些榜单、是否延续、难度怎么变成档位、各榜单试了什么，以及比较里被跳过的模型和原因（只进 trace，`Pick` 里的这部分字段不导出，消费日志的摘要不变大）；`Policy.ExplainUnroutable` 按原因汇总 |
| `model/auto_route_log.go`、`service/auto_route_log.go`、`service/event_writer.go` | 选路日志表、按 `route_log_mode` 决定记不记、异步批量写入（与能力门控事件共用泛型 writer）、详情超过 32KB 时精简、过期清理（`purgeBefore`，也供能力门控用） |
| `controller/auto_route_log.go`、`controller/autorouter.go`、`controller/system_task_handlers.go`、`router/api-router.go` | 选路日志的列表和按请求 id 取单条；auto 管理接口（状态、有效榜单、立即刷新、恢复内置）；清理任务 `auto_route_log_cleanup`；访问令牌路由表登记上述接口 |
| `setting/autorouter_setting/` | 另外：jev 连接和三个榜单来源 key 改走选项（为空回退环境变量）；`route_log_mode`、`route_log_retention_days`；所有 `autorouter_setting.*` 写入前由服务端校验，避免存进去却因为解析失败永远不生效 |
| `web/src/features/system-settings/autorouter/` | 系统设置里的“Auto 路由”分类：基础、偏好与成本、判断与推理强度、榜单、选路日志五节；选路日志含记录设置、日志列表和选路过程抽屉（`route-log/`） |
| `pkg/autorouter/policy.go`、`trace.go` | 第 7.2 节的思考强度顺序：沿用上一轮模型时按上一轮的档位提升，难度给的档位已经到了就不再加；`unknown` 且收益达到 `upgrade_benefit` 时照 `unknown` 的办法定模型、由判断定档位（`inherited` 为 false，覆盖客户端推理字段） |
| `web/src/features/usage-logs/` | 使用日志里标出 auto 请求并能按它筛选（`auto_routed` 列，老数据的空值按“不是 auto”算）；详情里的“Auto 路由”小节和“查看选路过程” |
| `model/channel_cache.go`、`model/ability.go` | `FirstAttemptChannelShares`：首次尝试落到各渠道的概率，与实际抽签共用 `retryTierLocked` / `databaseRetryTier` |
| `service/channel_affinity.go`、`service/capability_gate.go`、`service/autorouter.go` | 只读的 `PeekPreferredChannelByAffinity`、`KeptChannels`；`AutoServingModel` 是最终解析和比价共用的服务名规则；Scheduler 的 workload 改在选路之前写 |
| `service/scheduler_preview.go` | 调 Scheduler 的 `/v1/preview`：整次一个截止时间，只读熔断状态、失败不计入熔断；回 404 的地址一分钟内跳过、接着问下一个；一次最多问 64 个（不能比 Scheduler 的上限大，调大时先升级 Scheduler）；结果带状态写进选路留痕，问过没拿到回答时记 WARN |
| `pkg/autorouter/select.go`、`policy.go`、`trace.go` | `Candidates.Costs`：一次取齐比较成本；`Excluded` 的模型不参与比较；跳过原因 `scheduler_no_candidate`；`band` 多出成本惩罚、容量惩罚、成功率惩罚和 Utility 字段 |
| `middleware/auto_route_cost.go`、`middleware/distributor.go` | 实现 `Costs`：调度器预览或原生规则定渠道、按首次命中的分组报价、乘渠道费用倍率，并复用同一批渠道计算容量；预览说没有渠道的模型记进这次请求的拒绝原因 |
| `web/src/features/system-settings/autorouter/route-log/` | 候选表显示费用倍率、比较成本、成本惩罚、四个运行维度和 Utility；没用上调度器预览时在表上方说明原因，单个模型没用上时写在它的倍率下面 |
| `pkg/autorouter/metrics.go`、`service/auto_model_metrics.go`、`controller/relay.go` | 按 base model 记录五分钟结果样本；Redis 跨实例共享，读取失败退回节点内存；客户端取消、参数错误、上下文超限和额度耗尽不计结果样本 |
| `pkg/autorouter/jev.go`、`setting/autorouter_setting/ranking_edit.go`、`config.go` | 第 8.1 节：`RankingProfiles()` 列出全部画像；`EditedRanking` 校验整份手工榜单、与当前榜单比出改动，返回要写的四项榜单和关掉的定时刷新；`Ranking.Profiles()` 是读写共用的画像列表；`encodeOptions` 与恢复内置共用 |
| `controller/autorouter.go`、`controller/audit.go`、`router/api-router.go`、`middleware/access_token_routes.go` | `PUT /api/autorouter/ranking`（刷新进行中拒绝、审计 `autorouter.rank_edit`、访问令牌作用域 `option:write`）；`GET /api/autorouter/ranking` 按全部画像列出并给每个模型加 `served` |
| `web/src/features/system-settings/autorouter/` | 榜单编辑（`ranking-viewer.tsx`、`ranking-table.tsx`、`ranking-add-dialog.tsx`，纯逻辑在 `ranking-draft.ts`）；状态卡标出手工编辑过的榜单，立即刷新前确认 |

## 10. 已知限制

- **小榜覆盖有限**：`lb.*` 只含最新版本跑过的模型（`20261025` 为 31 个），`lma.document`、`lma.search` 各四十余个；两指标画像只要有其中一项即可上榜；`general.browser` 只用 `lb.browser`，只覆盖 LiveBench 收录的模型。未收录模型落到一级列表。
- **Artificial Analysis 免费接口只有指数**：单项 benchmark（如 Terminal-Bench、FrontierCode）需要付费接口，因此 `aa.*` 只用 Intelligence 指数（v5）和 Strategy & Ops 能力指数。接口仍返回 `coding_index`、`agentic_index` 字段，但它们已不在 v5 指数体系中、不再随新模型更新，不使用。Intelligence v5 只覆盖 2026-03 之后发布的模型。
- **LMArena 分榜新旧不一**：文档、搜索榜可能比文本榜旧一个月以上，以 `rank_sources` 记录的日期为准。
- **分类准确率待校准**：12 个二级类的判定标准需要用标注集验证 jev 的选择准确率。概率最高的二级类直接决定榜单，判错时就落在判错的那个榜单上。
- **四维权重是全局的**：不同画像的榜单分尺度不同，同一个 w 对选择的影响也不同。权重需要通过真实选路日志按画像观察，按画像分别标定还没做。
- **选路不再随机探索**：候选按统一效用分降序排列，第一名确定选中。容量和成功率变化会直接改变效用并触发首选换位。
- **容量取决于渠道上限**：配置了 RPM、TPM 或并发上限时才有对应的余量信号。硬可用性由渠道过滤和调度预览确定。
- **偏好只管选哪个模型**：思考强度由难度、上一轮的档位和大模型收益决定（第 7.2 节），不随偏好变；Scheduler 的路由倾向也与它无关。对话中途换偏好，要到下一个新任务才生效（同任务延续沿用上一次的模型）。
- **单指标画像的分数偏薄**：`general.browser`、`general.research` 只有一个指标；`general.design`、`general.search`、`general.writing` 在 2026-10-28 的快照里因 `da.slides`、`lma.search`、`lma.document` 停更，实际只剩一到两个有效指标。用很小的分差换很大的价差，依据比多指标画像弱。
- **提档受模型声明的档位限制**：期望档位按模型渠道声明的档位收敛，声明里最高只到 `high` 的模型，期望档位提到 `xhigh` 也按 `high` 发出。提档只改思考强度，不换更强的模型。
- **OpenAI / Codex 渠道还受网关的推理强度上限限制**：系统设置（模型 → 推理强度策略，`global.gpt_request_policy.reasoning_policy`）设了上限时，转发层把请求体里高于上限的档位降到上限，计费和消费日志按降后的档位记，所以不会超过上限。auto 定档时不看这个上限：期望档位是 `max` 时，走 OpenAI / Codex 渠道实际按 `xhigh` 发出，选路日志里"实际发送的推理强度"记的是 auto 写进请求体的档位，不含这一步。
- **比价用的是平台成本**：运行时按第 7.3 节比较“用户侧价格 × 首次会落到的渠道的费用倍率”。榜单文件里的 `price_per_mtok` 是 OpenRouter 的价格，只用来给 `default_models` 排序和本文示例，和运行时的价格可能差很多。
- **成本优先偏向较低的平台成本**：用户价 10 美元、倍率 0.2 的模型，其比较成本是 2 美元。用户价 3 美元、倍率 1 的模型，其比较成本是 3 美元。质量和运行状态相同时，前者效用更高，用户仍按 10 美元的报价计费。
- **只估首次尝试**：同一档里有几个渠道时用命中概率的期望，单次请求实际落到的渠道倍率可能不同；重试走原生选路、可能换渠道，不在估算里。
- **预览与正式调度之间隔着几毫秒**：准入是只读的，这期间别的请求改了计数池用量，两次可能选不同的渠道，`band` 里的 `channel_id` 和使用日志里实际的渠道对照就能量出这个比例。预览给出的渠道不会当作偏好渠道传给正式调度（那会被 Scheduler 记成会话亲和命中）。没被 Scheduler 加载过的模型先按原生规则估，被真实流量加载后才走预览。
- **能力收窄按撇开客户端推理字段的口径估**：档位由判断定下时，最终写进请求体的档位还会再收窄一次渠道，估算时还不知道这个档位，个别渠道可能估进去了、实际却被淘汰。
- **手工编辑与定时刷新二选一**：保存手工编辑会关掉定时刷新，公开榜单的新模型和新分数从此进不来；之后任何一次刷新或恢复内置都会把手工改动整个换掉。手工给的分数是一次性的快照，公开榜单变了不会跟着变。
- **编辑页的“网关有”和可选范围只是粗估**：只看渠道启用与否，不看令牌白名单、分组、能力声明和价格，一个请求实际能不能选中某个模型以选路日志为准。
- **多节点下保存有几秒延迟**：处理保存请求的节点立刻生效，其他节点等下一次选项同步（`SYNC_FREQUENCY`，默认 60 秒），这期间仍按旧榜单选路。冲突检查比的是处理请求那个节点上的榜单，两位管理员在同步间隔内通过不同节点各自保存时，后一次可能没被拦住。
