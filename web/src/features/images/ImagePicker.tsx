import { useRef, useState, type ClipboardEvent, type DragEvent, type ReactNode } from 'react'
import { LoaderCircleIcon, PlusIcon, XIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { DraftImagePreview } from './ImagePreview'
import { imageMimeTypes, imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from './image-drafts'
// When a function is passed as children, the add button and the thumbnail strip are handed to it to place inside the composer
// (addClassName adjusts the button size; the strip is null when there are no images); otherwise both go below.
// uploads is the upload state of each image while sending, shown on the matching thumbnail.
// refused: why images can't be added at all (the model takes none); the plus is disabled and says it, and a pasted or dropped image gets
// it as the error.
export function ImagePicker({ images, onChange, limits, disabled, refused, uploads, addClassName, children }: { images: ImageDraft[]; onChange: (images: ImageDraft[]) => void; limits: ImageLimits; disabled?: boolean; refused?: string | null; uploads?: ImageUploads; addClassName?: string; children?: ReactNode | ((addButton: ReactNode, strip: ReactNode) => ReactNode) }) {
  const input = useRef<HTMLInputElement>(null)
  const [error, setError] = useState(''), [dragging, setDragging] = useState(false)
  const add = (files: File[]) => {
    if (disabled || !files.length) return
    if (refused) return setError(refused)
    const problem = imageProblem(images, files, limits); setError(problem ?? '')
    if (!problem) onChange([...images, ...files.map(file => ({ id: crypto.randomUUID(), file }))])
  }
  const paste = (event: ClipboardEvent) => { const files = Array.from(event.clipboardData.files); if (files.length && !disabled) { event.preventDefault(); add(files) } }
  const drag = (event: DragEvent) => { if (event.dataTransfer.types.includes('Files')) { event.preventDefault(); if (!disabled && !refused) setDragging(true) } }
  const addButton = (
    <Button
      type="button"
      variant="secondary"
      size="icon-sm"
      className={cn(
        'border border-hairline-strong bg-surface-subtle text-ink-700 hover:bg-surface-hover hover:text-ink',
        addClassName,
      )}
      aria-label={t.images.add}
      title={refused ?? t.images.add}
      disabled={disabled || Boolean(refused)}
      onClick={() => input.current?.click()}
    >
      <PlusIcon aria-hidden="true" className="size-4" />
    </Button>
  )
  // The thumbnail strip scrolls horizontally inside the composer, so it doesn't widen the page even on narrow screens; the ×
  // sits on the thumbnail's corner and is always visible.
  const strip = images.length > 0 ? <div className="relative min-w-0">
    <ul aria-label={t.images.preview} className="flex min-w-0 gap-2 overflow-x-auto px-1 pt-1 pb-2">{images.map((image, index) => {
      const upload = uploads?.[image.id]
      return <li key={image.id} className="relative size-[60px] shrink-0">
        <DraftImagePreview file={image.file} thumb />
        {upload === 'uploading' && (
          <span className="pointer-events-none absolute inset-0 grid place-items-center rounded-ticket bg-surface-canvas/70">
            <LoaderCircleIcon aria-hidden="true" className="riso-spin size-5 text-ink motion-reduce:animate-none" strokeWidth={2.4} />
            <span className="sr-only">{t.images.uploading}</span>
          </span>
        )}
        {upload === 'failed' && (
          <span className="pointer-events-none absolute inset-0 flex items-end rounded-ticket ring-2 ring-red ring-inset">
            <span className="w-full rounded-b-ticket bg-red py-px text-center text-[11px] leading-4 font-semibold text-white">{t.images.uploadFailed}</span>
          </span>
        )}
        <button
          type="button"
          className="absolute top-1 right-1 grid size-5 cursor-pointer place-items-center rounded-full bg-cta-bg/85 text-cta-fg outline-none hover:bg-cta-bg focus-visible:shadow-focus disabled:cursor-default disabled:opacity-40 disabled:hover:bg-cta-bg/85"
          disabled={disabled}
          aria-label={t.images.remove(index + 1)}
          title={t.images.remove(index + 1)}
          onClick={() => {
            onChange(images.filter(item => item.id !== image.id))
            setError('')
          }}
        >
          <XIcon aria-hidden="true" className="size-3" strokeWidth={3} />
        </button>
      </li>
    })}</ul>
    {Object.values(uploads ?? {}).includes('uploading') && <p role="status" className="sr-only">{t.images.loading}</p>}
  </div> : null
  return <div onPaste={paste} onDragOver={drag} onDragLeave={event => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) setDragging(false) }} onDrop={event => { if (event.dataTransfer.types.includes('Files')) { event.preventDefault(); setDragging(false); add(Array.from(event.dataTransfer.files)) } }} className={cn('grid min-w-0 grid-cols-[minmax(0,1fr)] gap-3', dragging && 'rounded-panel ring-1 ring-ink')}>
    {typeof children === 'function' ? children(addButton, strip) : <>{children}{strip}</>}
    <input ref={input} type="file" accept={imageMimeTypes.join(',')} multiple aria-label={t.images.selectFile} className="sr-only" disabled={disabled} onChange={event => { add(Array.from(event.target.files ?? [])); event.target.value = '' }} />
    {typeof children !== 'function' && <div className="flex flex-wrap items-center gap-3">{addButton}</div>}
    {error && <p role="alert" className="text-sm text-red-text">{error}</p>}
  </div>
}
