import { api, ApiError, call, type Schemas } from './client'
import { t } from '@/i18n'
import { config } from '@/config'
import type { ImageDraft, ImageLimits, ImageUploads } from '@/features/images/image-drafts'
export type ImageScope = { kind: 'agent'; agentId: string } | { kind: 'task'; taskId: string; nodeRunId: string }
export function messageImageLimits(limits?: Schemas['Limits']): ImageLimits | null {
  const maxCount = limits?.maxMessageImages, maxImageBytes = limits?.maxMessageImageBytes
  if (typeof maxCount !== 'number' || typeof maxImageBytes !== 'number' || !Number.isSafeInteger(maxCount) || !Number.isSafeInteger(maxImageBytes) || maxCount <= 0 || maxImageBytes <= 0) return null
  if (!Number.isSafeInteger(maxCount * maxImageBytes)) return null
  return { maxCount, maxImageBytes, maxTotalBytes: maxCount * maxImageBytes }
}
// Each local image has one stable, scope-bound ID. Retrying a failed upload uses
// the same PUT/content, never a new upload or a message on another target.
// onUploads reports the batch's state for the thumbnails: images not yet uploaded
// are uploading; on an error only the image that failed stays marked.
export async function uploadMessageImages(scope: ImageScope, images: ImageDraft[], onUploads?: (uploads: ImageUploads) => void) {
  const refs: Schemas['MessageImage'][] = [], left = images.map(image => image.id)
  const report = () => onUploads?.(Object.fromEntries(left.map(id => [id, 'uploading'])))
  report()
  for (const image of images) {
    const body = { body: '', headers: { 'Content-Type': image.file.type }, bodySerializer: () => image.file }
    try {
      const ref = scope.kind === 'agent'
        ? await call(api.PUT('/agents/{agentId}/message-images/{imageId}', { params: { path: { agentId: scope.agentId, imageId: image.id } }, ...body }))
        : await call(api.PUT('/tasks/{taskId}/nodes/{nodeRunId}/message-images/{imageId}', { params: { path: { taskId: scope.taskId, nodeRunId: scope.nodeRunId, imageId: image.id } }, ...body }))
      if (ref.id !== image.id || ref.size !== image.file.size || ref.mimeType !== image.file.type) throw new ApiError({ status: 502, code: 'unknown', serverMessage: t.images.receiptMismatch })
      refs.push(ref)
    } catch (error) { onUploads?.({ [image.id]: 'failed' }); throw error }
    left.shift(); report()
  }
  return refs
}
export const messageImageURL = (id: string) => `${config.apiBase}/message-images/${encodeURIComponent(id)}`

// 编辑撤回的排队消息时取回它的图片：图片属于原消息，重新发送时要以新 ID 上传，所以读出内容作为新的待发送图片。
export async function imageDraftsOf(images: Schemas['MessageImage'][]): Promise<ImageDraft[]> {
  return Promise.all(images.map(async (image, index) => {
    const blob = await call(api.GET('/message-images/{imageId}', { params: { path: { imageId: image.id } }, parseAs: 'blob' }))
    const extension = image.mimeType.split('/')[1] ?? 'png'
    return { id: crypto.randomUUID(), file: new File([blob], `${index + 1}.${extension}`, { type: image.mimeType }) }
  }))
}
