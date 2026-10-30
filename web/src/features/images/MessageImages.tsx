import type { Schemas } from '@/api/client'
import { messageImageURL } from '@/api/message-images'
import { t } from '@/i18n'
import { ImagePreview } from './ImagePreview'
export function MessageImages({ images }: { images?: Schemas['MessageImage'][] }) {
  if (!images?.length) return null
  return <ul aria-label={t.images.history} className="mt-2 flex flex-wrap gap-3">{images.map((image, index) => <li key={image.id}><ImagePreview src={messageImageURL(image.id)} name={t.images.name(index + 1, image.width, image.height)} /></li>)}</ul>
}
