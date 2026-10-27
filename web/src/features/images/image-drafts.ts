import { imageText } from '@/i18n/images'
// Local composer state, not a wire DTO. Files stay tied to their original draft.
export type ImageDraft = { id: string; file: File }
export type ImageLimits = { maxCount: number; maxImageBytes: number; maxTotalBytes: number }
// 发送时每张图片的上传状态，显示在缩略图上；传完的图片不再标记。
export type ImageUploads = Partial<Record<string, 'uploading' | 'failed'>>
export const imageMimeTypes = ['image/png', 'image/jpeg', 'image/webp'] as const
export function imageProblem(existing: ImageDraft[], incoming: File[], limits: ImageLimits): string | null {
  if (incoming.some(file => !imageMimeTypes.some(type => file.type === type))) return imageText.only
  if (incoming.some(file => file.size === 0)) return imageText.empty
  if (existing.length + incoming.length > limits.maxCount) return imageText.tooMany(limits.maxCount)
  if (incoming.some(file => file.size > limits.maxImageBytes)) return imageText.tooLarge(limits.maxImageBytes)
  if ([...existing.map(image => image.file), ...incoming].reduce((total, file) => total + file.size, 0) > limits.maxTotalBytes) return imageText.tooTotal(limits.maxTotalBytes)
  return null
}
