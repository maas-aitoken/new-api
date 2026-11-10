package autorouter

import (
	"slices"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// EffortAction 是 auto 对请求里推理字段的处置。
type EffortAction string

const (
	// EffortKeep 不碰客户端写的推理字段：兜底路径，或模型没有推理声明。
	EffortKeep EffortAction = "keep"
	// EffortSet 把收敛后的档位写进请求体。
	EffortSet EffortAction = "set"
	// EffortStrip 模型明确不支持推理，去掉客户端带的推理字段。
	EffortStrip EffortAction = "strip"
)

// Protocol 是入站请求的协议，决定档位写进请求体的哪个字段。
type Protocol string

const (
	ProtocolChat      Protocol = "chat"
	ProtocolResponses Protocol = "responses"
	ProtocolAnthropic Protocol = "anthropic"
)

// ChannelReasoning 是一个候选渠道对选中模型的推理声明（已合并渠道收窄）。
type ChannelReasoning struct {
	// Declared 为 false 表示声明里完全没有推理维度，这个渠道不参与汇总。
	Declared bool
	// Unsupported 是声明了 supported=false。
	Unsupported bool
	// Mandatory 是关不掉思考，可用档位里不含 none。
	Mandatory bool
	// Efforts 是声明的档位集合，空表示不限档位。
	Efforts []string
}

// EffortSupport 汇总候选渠道对推理档位的声明，零值表示没有任何声明。
type EffortSupport struct {
	declared  bool
	supported bool
	allowed   []string
}

// Add 把一个渠道的声明并进汇总。
func (s *EffortSupport) Add(channel ChannelReasoning) {
	if !channel.Declared {
		return
	}
	s.declared = true
	if channel.Unsupported {
		return
	}
	s.supported = true
	efforts := channel.Efforts
	if len(efforts) == 0 {
		// 不限档位：阶梯上的都可用，声明了 mandatory 就不含 none。
		efforts = slices.DeleteFunc(slices.Clone(effortLadder), func(effort string) bool {
			return channel.Mandatory && effort == "none"
		})
	}
	for _, effort := range efforts {
		if !slices.Contains(s.allowed, effort) {
			s.allowed = append(s.allowed, effort)
		}
	}
}

// Clamp 把期望档位收敛到声明允许的集合里，返回要写的档位和处置方式：
//   - 没有任何渠道声明过推理：EffortKeep，客户端字段原样保留；
//   - 声明过但全都明确不支持：EffortStrip；
//   - 否则取集合里离期望档位最近的一档，距离相同取更强的，避免静默弱化推理。
func (s EffortSupport) Clamp(desired string) (string, EffortAction) {
	switch {
	case !s.declared:
		return "", EffortKeep
	case !s.supported:
		return "", EffortStrip
	case slices.Contains(s.allowed, desired):
		return desired, EffortSet
	}
	want := slices.Index(effortLadder, desired)
	if want < 0 {
		want = slices.Index(effortLadder, "medium")
	}
	best, bestDistance := "", 0
	for _, effort := range s.allowed {
		rank := slices.Index(effortLadder, effort)
		if rank < 0 {
			continue // 不在阶梯上的自定义档位没法比较远近
		}
		distance := max(rank-want, want-rank)
		if best == "" || distance < bestDistance || distance == bestDistance && rank > slices.Index(effortLadder, best) {
			best, bestDistance = effort, distance
		}
	}
	if best == "" {
		return "", EffortKeep
	}
	return best, EffortSet
}

// PatchBody 在入站请求体里一次写完 auto 的结论：模型名（model 为空则不改）和推理档位。
// 档位写进入站协议自己的字段，后面的协议转换和渠道适配照常处理它；
// 客户端写的互相冲突的字段会被一并清掉，保证请求体里只有 auto 定下的这一份。
func PatchBody(body []byte, protocol Protocol, model string, action EffortAction, effort string) ([]byte, error) {
	editor := bodyEditor{body: body}
	if model != "" {
		editor.set("model", model)
	}
	switch action {
	case EffortSet:
		editor.setEffort(protocol, effort)
	case EffortStrip:
		editor.stripEffort(protocol)
	}
	return editor.body, editor.err
}

// bodyEditor 顺序执行 sjson 编辑，第一个错误之后的编辑都跳过。
type bodyEditor struct {
	body []byte
	err  error
}

func (e *bodyEditor) set(path, value string) {
	if e.err == nil {
		e.body, e.err = sjson.SetBytes(e.body, path, value)
	}
}

func (e *bodyEditor) setRaw(path, raw string) {
	if e.err == nil {
		e.body, e.err = sjson.SetRawBytes(e.body, path, []byte(raw))
	}
}

func (e *bodyEditor) delete(paths ...string) {
	for _, path := range paths {
		if e.err == nil {
			e.body, e.err = sjson.DeleteBytes(e.body, path)
		}
	}
}

// dropIfEmpty 删掉删光子字段后留下的空对象。
func (e *bodyEditor) dropIfEmpty(path string) {
	if value := gjson.GetBytes(e.body, path); value.IsObject() && len(value.Map()) == 0 {
		e.delete(path)
	}
}

// writable 判断能否往 path 这个对象里写子字段：缺失、null 或对象都可以。
// 其他类型说明请求体本身就不合法，保持原样交给后面的校验去拒绝，不在这里悄悄改写。
func (e *bodyEditor) writable(path string) bool {
	value := gjson.GetBytes(e.body, path)
	return !value.Exists() || value.Type == gjson.Null || value.IsObject()
}

func (e *bodyEditor) setEffort(protocol Protocol, effort string) {
	switch protocol {
	case ProtocolResponses:
		if e.writable("reasoning") {
			e.set("reasoning.effort", effort)
		}
	case ProtocolAnthropic:
		if !e.writable("output_config") {
			return
		}
		if effort == "none" {
			e.delete("output_config.effort")
			e.dropIfEmpty("output_config")
			e.setRaw("thinking", `{"type":"disabled"}`)
			return
		}
		// thinking 交给转换层按目标模型渲染（adaptive 或预算），这里只写档位。
		e.delete("thinking")
		e.set("output_config.effort", effort)
	default:
		if gjson.GetBytes(e.body, "reasoning").IsObject() {
			e.delete("reasoning.effort", "reasoning.enabled", "reasoning.max_tokens")
			e.dropIfEmpty("reasoning")
		}
		e.set("reasoning_effort", effort)
	}
}

func (e *bodyEditor) stripEffort(protocol Protocol) {
	switch protocol {
	case ProtocolResponses:
		e.delete("reasoning")
	case ProtocolAnthropic:
		e.delete("thinking", "output_config.effort")
		e.dropIfEmpty("output_config")
	default:
		e.delete("reasoning_effort", "reasoning")
	}
}
