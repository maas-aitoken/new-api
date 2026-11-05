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
import { useQuery } from '@tanstack/react-query'

import { requireServerSuccess } from '@/lib/server-error-message'

import { getChannelModelCapacity } from '../api'
import { channelModelCapabilityQueryKeys } from '../lib'

// 实时用量只在模型行展开时轮询（行收起后组件卸载，查询随之停止）。
const CAPACITY_REFETCH_MS = 5000

/**
 * 一个模型在其所有服务渠道上的实时用量与剩余量，每 5 秒刷新。
 * 轮询失败不弹 Toast，由调用方显示一行提示。
 */
export function useChannelModelCapacity(
  modelName: string,
  channelIds: number[]
) {
  return useQuery({
    queryKey: channelModelCapabilityQueryKeys.capacity(modelName, channelIds),
    queryFn: async () => {
      const response = requireServerSuccess(
        await getChannelModelCapacity({
          model: modelName,
          channel_ids: channelIds,
        })
      )
      return response.data
    },
    enabled: channelIds.length > 0,
    refetchInterval: CAPACITY_REFETCH_MS,
    placeholderData: (previous) => previous,
    meta: { errorToast: false },
  })
}
