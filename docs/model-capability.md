# 模型能力声明（capabilities JSON）

模型对"能接受什么样的请求"的硬约束声明。分两层：

| 层级 | 存放位置 | 作用 |
| --- | --- | --- |
| 模型级 | `models.capabilities`（模型管理里每个模型一列） | 该对外模型全部能力的基线 |
| 渠道收窄 | `channel_model_capabilities`（能力设置页） | 某个具体渠道对该模型能力的收窄，只能是模型级的子集 |

渠道收窄的键是**渠道 Models 里声明的公开模型名**，不是映射后的上游名。两层都是同一份 JSON 结构，渠道层只写要收紧的字段。

模型级声明可以写在精确行，也可以写在前缀/后缀/包含规则行上：族内没有精确行的具体模型会继承该规则行的声明（同一规则内最长匹配键优先）。

相关代码：

| 位置 | 作用 |
| --- | --- |
| `service/capability/spec.go` | 结构定义、`Parse`/`Validate`、`Merge`、`CheckSubset`、`Summarize` |
| `service/capability/needs.go` | 四种协议（OpenAI Chat / Responses / Anthropic / Gemini）的请求解析 |
| `service/capability/check.go` | `Evaluate`：请求诉求与声明比对，产出违规项 |
| `service/capability/parameters.go` | 由声明派生模型广场的「支持的参数」表 |
| `model/channel_model_capability.go` | 声明解析优先级、缓存、收窄保存校验、模型级改动冲突校验 |
| `service/capability_gate.go` | 请求级门控、渠道过滤、失败原因、事件组装 |
| `service/capability_gate_event_writer.go` | 事件的异步批量写入（队列、批量、丢弃计数、退出时写完） |
| `model/capability_gate_event.go` | 事件表（日志库）、批量写入、查询、分块清理 |
| 管理界面 | 模型管理 → Models（模型级）/ Capabilities（渠道收窄）；系统设置 → 模型 → Capability Gate（门控配置与日志） |

## 1. 通用规则

- **未声明即不校验**：空字符串或 `{}` 表示该模型（或该渠道）不做任何能力校验。字段不写等价于"不限制"。
- **未知字段直接拒绝**：`Parse` 会用反射比对结构体，拼错的字段名报 `xxx: unknown field`，保存时返回错误，不会被静默忽略。
- **`*bool` 三态**：不写 = 不校验；`true` = 支持；`false` = 明确不支持，请求命中即违规。唯一例外是 `tools.builtin`：数组里没写就是不支持。
- **收窄只能更严格**：渠道收窄只能出现在模型级已声明的维度上，并且不能放大——
  - 布尔只能收紧成 `false`（模型级没声明过的维度，渠道不能自行声明）；
  - 枚举（`formats`/`sources`/`output`/`tool_choice`/`builtin`/`efforts`）必须是模型级的子集；渠道里写 `[]` 对这些枚举等于没写、继承模型级（空数组表示"未枚举"，不是"一个都不允许"），**唯一例外是 `builtin`**：渠道写 `"builtin": []` 表示该渠道一个内置工具都不支持，不写才是继承。想整体禁用其它维度，用对应的 `supported: false`；
  - 区间必须收在模型级区间之内；
  - 计数值（`stop.max`/`n.max`/`max_count`/`max_size_mb`/`max_tools`）只能更小；
  - `messages.system_role` 必须与模型级完全相同。
- **模型级收紧会被挡**：保存模型级声明时，会按"保存后"的归属重新检查所有存量渠道收窄；若某条收窄已越界，保存被拒绝并列出 `channel #N / 模型: 维度 ...`。清空模型级声明（改成空串）不走这项检查。
- **声明优先级**：精确行 > 前缀匹配 > 后缀匹配 > 包含匹配；同一规则内**最长匹配键优先**。元数据（厂商/描述/定价来源）与能力声明使用同一套优先级。

## 2. 字段参考

判定一列的「拦」表示会写下违规并（在 enforce 模式下）淘汰渠道；「记」表示默认只记录不拦截（可在门控设置里改）。

### 2.1 `context` —— 上下文与输出长度（单位 token）

| 字段 | 类型 | 约束 | 判定 |
| --- | --- | --- | --- |
| `max_input_tokens` | 整数 | > 0 | 估算输入超过即违规（拦） |
| `max_output_tokens` | 整数 | > 0 | 请求 `max_tokens`/`max_completion_tokens`/`max_output_tokens` 取最大者比对（拦） |

### 2.2 `modalities` —— 输入/输出模态

`input` 下有 `text`（自身是 `*bool`）和 `image`/`audio`/`video`/`file` 四个同构对象：

| 字段 | 类型 | 取值 | 判定 |
| --- | --- | --- | --- |
| `supported` | 布尔 | — | `false` 且请求带该模态 → 违规（拦） |
| `formats` | 字符串数组 | MIME，如 `image/png`、`application/pdf` | 非空时请求 MIME 必须在列表内（拦） |
| `sources` | 字符串数组 | `url` / `base64` / `file_id` | 非空时传入方式必须在列表内（拦） |
| `max_count` | 整数 | > 0 | 单次请求该模态媒体数量上限（拦） |
| `max_size_mb` | 整数 | > 0 | 单个媒体体积上限；只给 URL、体积未知时不判（拦） |

`modalities` 另有一个同级字段：

| 字段 | 类型 | 取值 | 判定 |
| --- | --- | --- | --- |
| `output` | 字符串数组 | `text` / `image` / `audio` | **不参与请求判定**，仅做收窄子集校验与广场展示 |

说明：模型级声明 `image.supported: true` 但没有枚举 `formats` 时，任何图片 MIME 都放行；想限制格式必须显式列出。

### 2.3 `tools` —— 工具调用

| 字段 | 类型 | 取值 | 判定 |
| --- | --- | --- | --- |
| `supported` | 布尔 | — | `false` 且请求带 tools → 违规（拦） |
| `parallel` | 布尔 | — | `false` 且客户端请求并行调用 → 违规（记） |
| `tool_choice` | 字符串数组 | `auto` / `none` / `required` / `named` | 非空时请求的 tool_choice 模式必须在列表内（拦） |
| `builtin` | 字符串数组 | `web_search` / `code_execution` / `file_search` / `computer_use` | 请求用到未列出的内置工具 → 违规（拦）；**未声明即不支持**。渠道收窄里不写 = 继承模型级，`[]` = 该渠道没有任何内置工具 |
| `max_tools` | 整数 | > 0 | 单次请求声明的工具数量上限（拦） |

自定义函数（`type` 为空或 `function`）计入 `max_tools`，其它 `type` 视为供应商内置工具，按 `builtin` 校验。

### 2.4 `structured_output` —— 结构化输出

| 字段 | 类型 | 判定 |
| --- | --- | --- |
| `json_mode` | 布尔 | `false` 且请求 `response_format.type=json_object` → 违规（拦） |
| `json_schema` | 布尔 | `false` 且请求 `response_format.type=json_schema` → 违规（拦） |

### 2.5 `reasoning` —— 思考能力

| 字段 | 类型 | 约束 | 判定 |
| --- | --- | --- | --- |
| `supported` | 布尔 | — | `false` 且请求要开启思考 → 违规（拦） |
| `mandatory` | 布尔 | 为 `true` 时 `efforts` 不得包含 `none` | `true` 且请求要求关闭思考（`effort=none`、`budget=0`、`enabled=false`）→ 违规（拦） |
| `efforts` | 字符串数组 | 自由字符串，常用 `none`/`minimal`/`low`/`medium`/`high`/`xhigh` | 非空时请求的 effort 必须精确命中集合；`none` 也按档位校验（拦） |
| `budget_tokens` | `{"min","max"}` 整数闭区间 | `min >= 0`、`min <= max` | 请求的思考预算 > 0 时必须落在区间内（拦） |
| `output_field` | 字符串 | — | 思考内容在响应里的回显字段名，**仅供展示** |

### 2.6 `params` —— 采样参数

| 字段 | 类型 | 约束 | 判定 |
| --- | --- | --- | --- |
| `temperature` | `{"min","max"}` 浮点闭区间 | `min <= max` | 请求 temperature 越界（记） |
| `top_p` | `{"min","max"}` 浮点闭区间 | `min <= max` | 请求 top_p 越界（记） |
| `top_k` | `{"min","max"}` 整数闭区间 | `min >= 0`、`min <= max` | 请求 top_k 越界（记） |
| `seed` | 布尔 | — | `false` 且请求带 seed（记） |
| `logprobs` | 布尔 | — | `false` 且请求 `logprobs=true`（记） |
| `presence_penalty` | 布尔 | — | `false` 且请求给非 0 值（记） |
| `frequency_penalty` | 布尔 | — | `false` 且请求给非 0 值（记） |
| `stop` | `{"max"}` | `max > 0` | 停止序列条数上限（记） |
| `n` | `{"max"}` | `max > 0` | 单次生成候选数上限，Gemini 对应 `candidateCount`（记） |

采样参数的区间只登记值域，**不做裁剪**：越界请求要么按策略记录、要么被拒，不会自动改写数值。默认属"仅记录"，在 Capability Gate 面板把 `params` 切成拦截即可生效。

### 2.7 `messages` —— 消息习惯

| 字段 | 类型 | 取值 | 判定 |
| --- | --- | --- | --- |
| `system_role` | 字符串 | `system` / `developer` / `none` | `none` 表示不支持 system 消息，请求带 system → 违规（拦）；收窄时必须与模型级相同 |
| `assistant_prefill` | 布尔 | — | `false` 且请求带 assistant 预填 → 违规（拦） |
| `multi_system` | 布尔 | — | `false` 且请求里 system 消息多于 1 条 → 违规（拦） |

### 2.8 `streaming` —— 流式

| 字段 | 类型 | 判定 |
| --- | --- | --- |
| `supported` | 布尔 | `false` 且请求 `stream=true` → 违规（拦） |
| `usage_in_stream` | 布尔 | **不参与请求判定**，仅做收窄子集校验 |

## 3. 完整示例

模型级声明（`models.capabilities`）：

```json
{
  "context": { "max_input_tokens": 200000, "max_output_tokens": 8192 },
  "modalities": {
    "input": {
      "text": true,
      "image": { "supported": true, "sources": ["url", "base64"], "max_count": 8, "max_size_mb": 20 },
      "file": { "supported": true, "sources": ["file_id"], "max_count": 4 }
    },
    "output": ["text"]
  },
  "tools": {
    "supported": true,
    "parallel": true,
    "tool_choice": ["auto", "none", "required"],
    "builtin": ["web_search"],
    "max_tools": 64
  },
  "structured_output": { "json_mode": true, "json_schema": true },
  "reasoning": {
    "supported": true,
    "mandatory": false,
    "efforts": ["none", "low", "medium", "high"],
    "budget_tokens": { "min": 1024, "max": 32000 }
  },
  "params": { "temperature": { "min": 0, "max": 2 }, "top_p": { "min": 0, "max": 1 }, "n": { "max": 1 } },
  "messages": { "system_role": "system", "multi_system": false },
  "streaming": { "supported": true, "usage_in_stream": true }
}
```

把某个渠道收窄成只支持 `low`/`medium`、温度不超过 1、不接图片：

```json
{
  "params": { "temperature": { "min": 0, "max": 1 } },
  "reasoning": { "efforts": ["low", "medium"] },
  "modalities": { "input": { "image": { "supported": false } } }
}
```

## 4. 判定与拦截策略

- 请求进入 relay 后，`needs.go` 按入口协议解析出诉求（工具数、模态、思考档位、采样参数……），模型级与渠道级各判一次：
  - 模型级：只用于 `auto` 候选筛选；
  - 渠道级：选定模型后对所有非 pinned 候选渠道做过滤，淘汰结果同时注册进重试链。
- 违规分 hard / soft。哪些维度只记录，在「系统设置 → 模型 → Capability Gate」的**维度策略**里按维度组切换（`capability_gate_setting.record_only_dimensions`，默认 `params,tools.parallel`）。清单清空 = 所有维度都拦截。
- 门控模式（同一面板）：`off` 不评估；`shadow` 只评估并留痕，不改选路；`enforce` 真正淘汰渠道。模式、请求体上限、维度策略、保留期都存 DB，没有环境变量开关（只有下面事件 writer 的进程级调优项是环境变量）。
- 渠道级判定会写入 `capability_gate_events`：`passed` / `soft` / `filtered`（淘汰部分渠道但请求仍完成）/ `rejected`（可用渠道为 0，请求被拦）/ `would_filter` / `would_reject`（shadow 预演）。「记录通过的判定」**默认关闭**，只留违规行；打开后每个请求写一行，只适合短时间排查。
- 事件是遥测，不在请求路径上落库：`service/capability_gate_event_writer.go` 用 4096 容量的队列接住事件，后台每 2 秒或满 200 条批量写入**日志库**（`LOG_SQL_DSN` 为空时与主库同库）。队列满了直接丢弃并计数，写入失败只记日志，都不影响选路。进程退出时会把队列写完。队列容量、每批条数、最长等待时间、清理任务每块删除的行数可以用 `CAPABILITY_GATE_EVENT_BUFFER_SIZE`、`CAPABILITY_GATE_EVENT_BATCH_SIZE`、`CAPABILITY_GATE_EVENT_FLUSH_MS`、`CAPABILITY_GATE_EVENT_CLEANUP_CHUNK` 调整（写法同 `RELAY_ATTEMPT_LOG_*`，超出范围会被截断，范围与默认值见 `.env.example`）。`rejected_channels` 的列宽跟表结构绑定，不可调。
- 每条事件带 `request_id`（与 `logs`、`relay_attempts` 同源）、`user_id`、`token_id`、`using_group`，管理页可以按 request_id 筛选，再去使用日志里看这次请求的真实结果。`rejected_channels` 列宽有限，过长时在逗号处截断，准确数量见 `rejected_count`。
- 保留期由「Gate log retention (days)」控制。非 ClickHouse 日志库由独立的系统任务 `capability_gate_event_cleanup` 每小时分块删除过期事件；ClickHouse 日志库靠建表 TTL（`LOG_SQL_CLICKHOUSE_TTL_DAYS`）过期，不走这个任务。
- 对被拦下的请求，错误信息会列出未满足的维度与实际声明值，例如
  `no available channel for model "x" satisfies the request: reasoning.effort (requested xhigh, supported none, low, medium)`。

## 5. 派生到模型广场

- **「支持的参数」表**由 `parameters.go` 从声明派生：`context.max_output_tokens`、`params.*`、`streaming.supported`、`structured_output.*`、`tools.*`、`reasoning.*` 会出现在表里，值域用声明值；未声明的维度不出现。默认值不展示（它取决于渠道与上游实现）。
- **能力标签**由 `Summarize()` 生成：`function_calling`、`streaming`、`vision`、`json_mode`、`structured_output`、`reasoning`、`web_search`、`code_interpreter`、`system_prompt`，另加输入/输出模态与 `max_output_tokens`。

## 6. 常见错误

| 报错 | 原因 |
| --- | --- |
| `xxx: unknown field` | 字段名拼错或用了不存在的维度，例如把 `structured_output` 写成 `structured` |
| `params.top_k: min 10 is greater than max 3` | 区间方向反了 |
| `reasoning.efforts: none cannot be listed when mandatory=true` | `mandatory` 与 `efforts` 含 `none` 自相矛盾 |
| `modalities.input.image: model-level does not declare this dimension` | 渠道收窄写了模型级没声明过的维度 |
| `params.temperature: [0,2] exceeds model-level [0,1]` | 收窄比模型级更宽 |
| `model-level declaration would no longer cover existing channel narrowings: channel #3 / m: ...` | 模型级被收紧后，存量渠道收窄越界；先调整该渠道收窄再保存 |