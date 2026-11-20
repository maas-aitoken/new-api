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
import type { RoutePick, RouteTraceMode } from './types'

export type RoutePresentationSchema = 'legacy' | 'current' | 'partial'

/** Persisted JSON is not runtime-validated; only positive shape markers select an algorithm. */
export function routePickSchema(pick: RoutePick): RoutePresentationSchema {
  if (pick.dimensions && typeof pick.dimensions === 'object') {
    return 'current'
  }
  const band = Array.isArray(pick.band) ? pick.band : []
  const hasLegacyEvidence =
    typeof pick.weight === 'number' ||
    band.some(
      (candidate) =>
        typeof candidate.discount === 'number' ||
        typeof candidate.capacity_premium === 'number'
    )
  return hasLegacyEvidence ? 'legacy' : 'partial'
}

export function routeModeSchema(mode: RouteTraceMode): RoutePresentationSchema {
  if (mode.weights && typeof mode.weights === 'object') {
    return 'current'
  }
  const tiers = Object.values(mode.tiers ?? {})
  if (tiers.some((tier) => tier && typeof tier === 'object')) {
    return 'partial'
  }
  if (
    typeof mode.weight === 'number' ||
    tiers.some((tier) => typeof tier === 'number')
  ) {
    return 'legacy'
  }
  return 'partial'
}
