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
import type { ResourcePoolProvider } from '../api'

/** 一个 provider 的容量池列表与实时余量共用前缀，保存或改成员后一起失效。 */
export const capacityPoolQueryKeys = {
  provider: (provider: ResourcePoolProvider) =>
    ['resource-pools', 'capacity-pools', provider] as const,
  list: (provider: ResourcePoolProvider) =>
    ['resource-pools', 'capacity-pools', provider, 'list'] as const,
  capacity: (provider: ResourcePoolProvider) =>
    ['resource-pools', 'capacity-pools', provider, 'capacity'] as const,
}
