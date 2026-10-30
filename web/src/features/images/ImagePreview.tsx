import { useEffect, useState } from 'react'
import { ImageOffIcon } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'
// thumb：输入框里的方形缩略图，裁切填满；默认是历史消息里的完整预览。
export function ImagePreview({ src, name, thumb }: { src: string; name: string; thumb?: boolean }) {
  const [open, setOpen] = useState(false), [failed, setFailed] = useState(false)
  return <><button type="button" aria-label={t.images.enlarge(name)} onClick={() => setOpen(true)} className={thumb ? 'block size-full cursor-zoom-in overflow-hidden rounded-ticket border border-ink/15 bg-paper outline-none focus-visible:shadow-focus' : 'overflow-hidden rounded-input border border-ink/20 bg-white p-1'}><img src={src} alt={name} onError={() => setFailed(true)} loading="lazy" className={thumb ? 'size-full object-cover' : 'h-20 w-28 object-contain'} />{failed && <span className={thumb ? 'sr-only' : 'block max-w-28 text-xs'}>{t.images.failed}</span>}</button><Dialog open={open} onOpenChange={setOpen}><DialogContent className="max-h-[90dvh] sm:max-w-[min(90vw,1200px)]" aria-describedby={undefined}><DialogTitle className="pr-10 break-words">{name}</DialogTitle><img src={src} alt={name} className="max-h-[70dvh] w-full object-contain" /></DialogContent></Dialog></>
}
export function DraftImagePreview({ file, thumb }: { file: File; thumb?: boolean }) {
  const [src, setSrc] = useState(''), [failed, setFailed] = useState(false)
  useEffect(() => {
    // Draft-only preview, released on unmount. History never reads this data URL.
    const reader = new FileReader()
    reader.onload = () => { if (typeof reader.result === 'string') setSrc(reader.result) }
    reader.onerror = () => setFailed(true)
    reader.readAsDataURL(file)
    return () => { reader.onload = null; reader.onerror = null; if (reader.readyState === FileReader.LOADING) reader.abort() }
  }, [file])
  if (failed) return thumb ? <p role="alert" title={t.images.failed} className="grid size-full place-items-center rounded-ticket border border-ink/15 bg-tint-red text-red-text"><ImageOffIcon aria-hidden="true" className="size-5" /><span className="sr-only">{t.images.failed}</span></p> : <p role="alert" className="text-xs">{t.images.failed}</p>
  return src ? <ImagePreview key={src} src={src} name={file.name} thumb={thumb} /> : thumb ? <span aria-hidden="true" className="block size-full rounded-ticket border border-ink/15 bg-paper" /> : null
}
