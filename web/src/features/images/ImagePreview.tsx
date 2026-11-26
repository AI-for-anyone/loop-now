import { useEffect, useState, type HTMLAttributeReferrerPolicy } from 'react'
import { ExternalLinkIcon, ImageOffIcon } from 'lucide-react'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'

// Shared lightbox preview for attached images and Markdown images in replies: fits the window, closes on Escape or outside click, and
// offers a new-tab link to the original for non-data/non-blob URLs (an explicit originalHref can point to a safe download URL when
// src is a local Blob URL, so SVG blobs are never opened as top-level documents).
export function ImagePreviewDialog({
  open,
  onOpenChange,
  src,
  alt,
  title,
  referrerPolicy,
  showOriginal = false,
  originalHref,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  src: string
  alt: string
  title: string
  referrerPolicy?: HTMLAttributeReferrerPolicy
  showOriginal?: boolean
  originalHref?: string
}) {
  const original = originalHref ?? src
  const canOpen = showOriginal && Boolean(original) && !/^(?:data|blob):/i.test(original)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent kind="view" className="max-h-[90dvh] sm:max-w-[min(90vw,1200px)]" aria-describedby={undefined}>
        <DialogTitle className="pr-10 break-words">{title}</DialogTitle>
        <img src={src} alt={alt} referrerPolicy={referrerPolicy} className="mx-auto max-h-[68dvh] max-w-full object-contain" />
        {canOpen && (
          <div className="flex justify-end">
            <a
              href={original}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 text-[13.5px] font-bold text-riso-blue underline decoration-2 underline-offset-2 outline-none focus-visible:shadow-focus"
            >
              {t.images.openOriginal}
              <ExternalLinkIcon aria-hidden="true" className="size-3.5 shrink-0" />
            </a>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// thumb: a square thumbnail in the composer, cropped to fill; by default it is the full preview in message history.
export function ImagePreview({ src, name, thumb }: { src: string; name: string; thumb?: boolean }) {
  const [open, setOpen] = useState(false), [failed, setFailed] = useState(false)
  return (
    <>
      <button
        type="button"
        aria-label={t.images.enlarge(name)}
        onClick={() => setOpen(true)}
        className={thumb ? 'block size-full cursor-zoom-in overflow-hidden rounded-ticket border border-ink/15 bg-paper outline-none focus-visible:shadow-focus' : 'overflow-hidden rounded-input border border-ink/20 bg-white p-1'}
      >
        <img src={src} alt={name} onError={() => setFailed(true)} loading="lazy" className={thumb ? 'size-full object-cover' : 'h-20 w-28 object-contain'} />
        {failed && <span className={thumb ? 'sr-only' : 'block max-w-28 text-xs'}>{t.images.failed}</span>}
      </button>
      <ImagePreviewDialog open={open} onOpenChange={setOpen} src={src} alt={name} title={name} showOriginal />
    </>
  )
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
