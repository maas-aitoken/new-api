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
import type { TFunction } from 'i18next'

import type { RouteResult } from './types'

export type RouteTone = 'success' | 'warning' | 'danger' | 'info' | 'neutral'

/** Leaderboard-style scores and probabilities keep four decimals. */
export function formatScore(value?: number | null): string {
  return value == null ? '-' : value.toFixed(4)
}

export function outcomeLabel(t: TFunction, outcome: string): string {
  return outcome === 'failed' ? t('Failed') : t('Routed')
}

export function outcomeTone(outcome: string): RouteTone {
  return outcome === 'failed' ? 'danger' : 'success'
}

export function judgmentLabel(t: TFunction, judgment: string): string {
  switch (judgment) {
    case 'live':
      return t('Classified just now')
    case 'live_failed':
      return t('Classifier failed')
    case 'cache':
      return t('Classification cached')
    case 'recent_failure':
      return t('Classifier failed recently')
    case 'session':
      return t('Followed the session')
    case 'auxiliary':
      return t('Background request')
    case 'no_instruction':
      return t('No instruction to classify')
    case 'unroutable':
      return t('No model available')
    default:
      return judgment
  }
}

export function judgmentNote(t: TFunction, judgment: string): string {
  switch (judgment) {
    case 'live':
      return t('The classifier was called for this request.')
    case 'live_failed':
      return t(
        'The classifier was called and failed, so the route follows the previous instruction or the default models.'
      )
    case 'cache':
      return t(
        'The same instruction was classified before, so the cached answer was reused and the classifier was not called.'
      )
    case 'recent_failure':
      return t(
        'The classifier failed moments ago and is not retried yet, so the route follows the previous instruction or the default models.'
      )
    case 'session':
      return t(
        'A sub-agent task without readable text follows the route of its session.'
      )
    case 'auxiliary':
      return t(
        'Background requests such as titles or suggestions are never classified.'
      )
    case 'no_instruction':
      return t('The request holds no readable instruction to classify.')
    case 'unroutable':
      return t(
        'None of the ranked models can serve this request, so the classifier was not called.'
      )
    default:
      return ''
  }
}

export function judgmentTone(judgment: string): RouteTone {
  switch (judgment) {
    case 'live':
      return 'success'
    case 'cache':
    case 'session':
      return 'info'
    case 'recent_failure':
      return 'warning'
    case 'live_failed':
    case 'unroutable':
      return 'danger'
    default:
      return 'neutral'
  }
}

export function skipReasonLabel(t: TFunction, reason: string): string {
  switch (reason) {
    case 'token_limit':
      return t("Not allowed by the key's model list")
    case 'group_disabled':
      return t('Not enabled in the group')
    case 'no_channel':
      return t('No enabled channel serves it')
    case 'unpriced':
      return t('No price configured')
    case 'capability':
      return t('Capabilities fall short of the request')
    case 'per_call':
      return t('Billed per call, so it cannot be compared by price')
    case 'client_reasoning':
      return t("Ruled out by the client's own reasoning settings")
    case 'unavailable':
      return t('Unavailable')
    default:
      return reason
  }
}

export function trustReasonLabel(t: TFunction, reason: string): string {
  switch (reason) {
    case 'no_decision':
      return t('The classifier gave no answer')
    case 'no_difficulty':
      return t('The answer carries no difficulty')
    case 'low_confidence':
      return t('No task type or class is likely enough')
    default:
      return reason
  }
}

export function ladderOutcomeLabel(t: TFunction, outcome: string): string {
  switch (outcome) {
    case 'compared':
      return t('Picked by quality and price')
    case 'first_available':
      return t('First servable model in list order')
    case 'none':
      return t('No servable model')
    case 'inherited':
      return t('Kept the previous route')
    default:
      return outcome
  }
}

export function routeSourceLabel(t: TFunction, source: string): string {
  switch (source) {
    case 'jev':
      return t('Classifier')
    case 'cache':
      return t('Cache')
    case 'inherit':
      return t('Previous route')
    case 'default':
      return t('Default models')
    default:
      return source
  }
}

/**
 * How the reasoning effort reached the model. A model that declares reasoning gets it
 * in the request body; any other carries it as a suffix of its name, and the bare name
 * means no variant existed, so the provider's own default effort applies.
 */
export function effortDeliveryNote(t: TFunction, result: RouteResult): string {
  if (result.effort_in_body) {
    switch (result.effort_action) {
      case 'set':
        return t("Written into the request, replacing the client's own setting.")
      case 'strip':
        return t(
          "The model does not support reasoning, so the client's reasoning settings were removed."
        )
      default:
        return t("The client's own reasoning settings were left untouched.")
    }
  }
  if (result.resolved_model && result.resolved_model !== result.model) {
    return t('Sent as part of the model name.')
  }
  return t('No model variant carries this effort, so the provider default applies.')
}

export function preferenceLabel(t: TFunction, preference: string): string {
  switch (preference) {
    case 'capability':
      return t('Capability first')
    case 'balanced':
      return t('Balanced')
    case 'cost':
      return t('Cost first')
    case 'adaptive':
      return t('Decide per request')
    default:
      return preference
  }
}

/** The gateway's own sentence is a locale key; anything else is shown as it came. */
export function routeErrorLabel(t: TFunction, error: string): string {
  if (error === 'auto router found no available model for this token and group') {
    return t('auto router found no available model for this token and group')
  }
  return error
}

/** The inbound API names are proper nouns, so they are not translated. */
export function protocolLabel(protocol?: string): string {
  switch (protocol) {
    case 'chat':
      return 'Chat Completions'
    case 'responses':
      return 'Responses'
    case 'anthropic':
      return 'Anthropic Messages'
    default:
      return protocol ?? '-'
  }
}
