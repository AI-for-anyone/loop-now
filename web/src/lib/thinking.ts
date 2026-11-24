import type { Schemas } from '@/api/client'

// The thinking levels a person can choose for a model. One level is no choice: Pi lists only off for a model that doesn't think,
// and a model whose level comes with it (Antigravity's, whose id carries it) or a local ACP command's one model lists only off too.
export function choosableLevels(model: Pick<Schemas['Model'], 'thinkingLevels'> | null | undefined): readonly Schemas['ThinkingLevel'][] {
  const levels = model?.thinkingLevels ?? []
  return levels.length > 1 ? levels : []
}
