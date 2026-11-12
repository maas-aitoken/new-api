import type { QueryClient } from '@tanstack/react-query'

export const modelSquareQueryKeys = {
  all: ['model-square'] as const,
  lists: () => [...modelSquareQueryKeys.all, 'list'] as const,
  list: (params: { offset: number; limit: number }) =>
    [...modelSquareQueryKeys.lists(), params] as const,
  details: () => [...modelSquareQueryKeys.all, 'detail'] as const,
  detail: (modelId: string | number) =>
    [...modelSquareQueryKeys.details(), String(modelId)] as const,
}

export function invalidateModelSquareQueries(
  queryClient: QueryClient | undefined
): Promise<void> {
  if (!queryClient) return Promise.resolve()
  return queryClient.invalidateQueries({ queryKey: modelSquareQueryKeys.all })
}
