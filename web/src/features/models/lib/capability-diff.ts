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
export interface CapabilitySpecDiffRow {
  path: string
  modelValue: string
  effectiveValue: string
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value)
}

function isRangeObject(value: Record<string, unknown>): boolean {
  const keys = Object.keys(value)
  if (
    keys.length === 2 &&
    'min' in value &&
    'max' in value &&
    typeof value.min === 'number' &&
    typeof value.max === 'number'
  ) {
    return true
  }
  return (
    keys.length === 1 && 'max' in value && typeof value.max === 'number'
  )
}

function formatSpecLeaf(value: unknown): string {
  if (value === undefined || value === null) {
    return '—'
  }
  if (typeof value === 'boolean') {
    return value ? 'Supported' : 'Not supported'
  }
  if (typeof value === 'number' || typeof value === 'string') {
    return String(value)
  }
  if (Array.isArray(value)) {
    return value.length > 0 ? value.map(String).join(', ') : 'None'
  }
  if (isPlainObject(value)) {
    if ('min' in value && 'max' in value) {
      return `${value.min} ~ ${value.max}`
    }
    if ('max' in value) {
      return `≤ ${value.max}`
    }
    const entries = Object.entries(value).filter(
      ([, item]) => item !== undefined
    )
    if (entries.length === 0) {
      return '—'
    }
    return JSON.stringify(value)
  }
  return String(value)
}

function collectLeafMap(
  node: unknown,
  prefix: string,
  out: Map<string, string>
) {
  if (node === undefined || node === null) return
  if (Array.isArray(node) || typeof node !== 'object') {
    if (prefix) out.set(prefix, formatSpecLeaf(node))
    return
  }
  const obj = node as Record<string, unknown>
  if (isRangeObject(obj)) {
    if (prefix) out.set(prefix, formatSpecLeaf(obj))
    return
  }
  for (const [key, child] of Object.entries(obj)) {
    const nextPath = prefix ? `${prefix}.${key}` : key
    collectLeafMap(child, nextPath, out)
  }
}

/**
 * 对比模型级能力 JSON 与渠道合并后的生效能力 JSON，返回实际发生收窄的叶子维度。
 */
export function diffCapabilitySpecs(
  modelRaw?: string,
  effectiveRaw?: string
): CapabilitySpecDiffRow[] {
  if (!modelRaw?.trim() || !effectiveRaw?.trim()) return []
  try {
    const modelSpec = JSON.parse(modelRaw)
    const effectiveSpec = JSON.parse(effectiveRaw)
    if (!isPlainObject(modelSpec) || !isPlainObject(effectiveSpec)) return []
    const baseLeaves = new Map<string, string>()
    const effectiveLeaves = new Map<string, string>()
    collectLeafMap(modelSpec, '', baseLeaves)
    collectLeafMap(effectiveSpec, '', effectiveLeaves)
    const allPaths = [
      ...new Set([...baseLeaves.keys(), ...effectiveLeaves.keys()]),
    ].sort()
    const rows: CapabilitySpecDiffRow[] = []
    for (const path of allPaths) {
      const modelValue = baseLeaves.get(path) ?? '—'
      const effectiveValue = effectiveLeaves.get(path) ?? '—'
      if (modelValue !== effectiveValue) {
        rows.push({ path, modelValue, effectiveValue })
      }
    }
    return rows
  } catch {
    return []
  }
}
