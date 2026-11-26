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
import { z } from 'zod'

import type {
  ResourcePoolAlertConfig,
  ResourcePoolAlertInput,
  ResourcePoolProvider,
} from '../api'

export const ALERT_PROVIDERS = [
  'claude',
  'gpt',
  'gemini',
  'grok',
] as const satisfies readonly ResourcePoolProvider[]

/** 与后端 DefaultResourcePoolAlertThreshold 等默认值一致。 */
const DEFAULT_THRESHOLD = 80
const DEFAULT_NOTIFY_START = '09:00'
const DEFAULT_NOTIFY_END = '21:00'
/** 与后端 resourcePoolAlertMaxMention 一致。 */
const MAX_MENTIONS_LENGTH = 512

/** 与后端 ValidateWeComGroupBotURL 一致：只接受企业微信群机器人的 webhook 地址。 */
export function isWeComGroupBotURL(value: string): boolean {
  try {
    const url = new URL(value)
    return (
      url.protocol === 'https:' &&
      url.host === 'qyapi.weixin.qq.com' &&
      url.pathname === '/cgi-bin/webhook/send' &&
      Boolean(url.searchParams.get('key'))
    )
  } catch {
    return false
  }
}

const clock = z
  .string()
  .regex(/^([01]\d|2[0-3]):[0-5]\d$/, 'Enter a time in HH:MM format')

const ruleSchema = z.object({
  enabled: z.boolean(),
  threshold: z.coerce
    .number<string | number>()
    .gt(0, 'Enter a percentage between 0 and 100')
    .max(100, 'Enter a percentage between 0 and 100'),
})

export const waterLevelAlertFormSchema = z
  .object({
    enabled: z.boolean(),
    /** 留空表示保留已保存的地址；表单里从不回填脱敏后的地址。 */
    webhook_url: z
      .string()
      .trim()
      .refine(
        (value) => value === '' || isWeComGroupBotURL(value),
        'Enter a valid WeCom group bot webhook URL'
      ),
    /** 只读：服务端是否已存有地址，不提交。 */
    webhook_configured: z.boolean(),
    mentions: z.string().trim().max(MAX_MENTIONS_LENGTH, 'Value is too large'),
    notify_start: clock,
    notify_end: clock,
    rules: z.object({
      claude: ruleSchema,
      gpt: ruleSchema,
      gemini: ruleSchema,
      grok: ruleSchema,
    }),
    /** 是否还单独检测每个 Claude 渠道；关闭时 Claude 只检测整体和分组。 */
    claude_source_enabled: z.boolean(),
  })
  .superRefine((values, ctx) => {
    if (!values.enabled) return
    if (!values.webhook_url && !values.webhook_configured) {
      ctx.addIssue({
        code: 'custom',
        path: ['webhook_url'],
        message: 'Set the webhook URL before enabling alerts',
      })
    }
    // 总开关开着但没有任何 provider 启用时什么都不会检测，保存前拦下来。
    // 挂在总开关上：rules 是嵌套对象，没有自己的输入框可以显示这条提示。
    if (!ALERT_PROVIDERS.some((provider) => values.rules[provider].enabled)) {
      ctx.addIssue({
        code: 'custom',
        path: ['enabled'],
        message: 'Enable alerts for at least one provider',
      })
    }
  })

export type WaterLevelAlertFormValues = z.infer<
  typeof waterLevelAlertFormSchema
>

/** 表单初值；配置还没加载出来时用后端的默认值。 */
export function waterLevelAlertFormDefaults(
  config: ResourcePoolAlertConfig | undefined
): WaterLevelAlertFormValues {
  const rule = (provider: ResourcePoolProvider) => ({
    enabled: config?.rules?.[provider]?.enabled ?? false,
    threshold: config?.rules?.[provider]?.threshold ?? DEFAULT_THRESHOLD,
  })
  return {
    enabled: config?.enabled ?? false,
    webhook_url: '',
    webhook_configured: config?.webhook_configured ?? false,
    mentions: config?.mentions ?? '',
    notify_start: config?.notify_start || DEFAULT_NOTIFY_START,
    notify_end: config?.notify_end || DEFAULT_NOTIFY_END,
    rules: {
      claude: rule('claude'),
      gpt: rule('gpt'),
      gemini: rule('gemini'),
      grok: rule('grok'),
    },
    claude_source_enabled: config?.claude_source_enabled ?? false,
  }
}

/** 把表单值转成保存接口要的请求体。 */
export function waterLevelAlertInputFromForm(
  values: WaterLevelAlertFormValues
): ResourcePoolAlertInput {
  return {
    enabled: values.enabled,
    webhook_url: values.webhook_url,
    mentions: values.mentions,
    notify_start: values.notify_start,
    notify_end: values.notify_end,
    rules: values.rules,
    claude_source_enabled: values.claude_source_enabled,
  }
}
