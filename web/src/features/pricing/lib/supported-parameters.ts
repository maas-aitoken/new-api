/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ModelParameter, PricingModel } from '../types'

export type SupportedParameter = {
  name: string
  type: ModelParameter['type']
  range?: string
  maxItems?: number
  enumValues?: string[]
  descriptionKey: string
  required?: boolean
}

type ProtocolParameterMeta = {
  descriptionKey: string
  required?: boolean
}

// 模型只声明"这个参数存在、值域多大"。默认值不在公开页面展示：
// 它取决于渠道与上游实现，不属于模型事实。
const PROTOCOL_PARAMETERS: Record<string, ProtocolParameterMeta> = {
  temperature: {
    descriptionKey: 'Sampling temperature; lower is more deterministic',
  },
  top_p: {
    descriptionKey: 'Nucleus sampling probability mass',
  },
  top_k: {
    descriptionKey: 'Limits sampling to the K most likely tokens',
  },
  max_tokens: {
    descriptionKey: 'Maximum number of tokens in the response',
  },
  frequency_penalty: {
    descriptionKey: 'Penalises repetition of frequent tokens',
  },
  presence_penalty: {
    descriptionKey: 'Encourages introducing new topics',
  },
  stop: {
    descriptionKey: 'Strings that stop generation when produced',
  },
  seed: {
    descriptionKey: 'Deterministic sampling seed (best-effort)',
  },
  n: {
    descriptionKey: 'Number of completions to generate',
  },
  stream: {
    descriptionKey: 'Stream tokens via Server-Sent Events',
  },
  response_format: {
    descriptionKey: 'Force JSON object or schema-conforming output',
  },
  tools: {
    descriptionKey: 'Tool / function declarations the model may call',
  },
  tool_choice: {
    descriptionKey: 'Tool-choice policy or specific tool name',
  },
  logprobs: {
    descriptionKey: 'Return per-token log probabilities',
  },
  reasoning_effort: {
    descriptionKey: 'Controls how much the model thinks before answering',
  },
  reasoning_budget_tokens: {
    descriptionKey: 'Maximum tokens the model may spend on reasoning',
  },
}

export function buildSupportedParameters(
  model: PricingModel
): SupportedParameter[] {
  return (model.parameters ?? []).map((parameter) => {
    const meta = PROTOCOL_PARAMETERS[parameter.name]
    return {
      name: parameter.name,
      type: parameter.type,
      range: parameter.range,
      maxItems: parameter.max_items,
      enumValues: parameter.enum_values,
      descriptionKey:
        meta?.descriptionKey ?? 'Declared by this model capability spec',
      required: meta?.required,
    }
  })
}