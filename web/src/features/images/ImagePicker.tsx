import { useRef, useState, type ClipboardEvent, type DragEvent, type ReactNode } from 'react'
import { LoaderCircleIcon, PlusIcon, XIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { imageText } from '@/i18n/images'
import { cn } from '@/lib/utils'
import { DraftImagePreview } from './ImagePreview'
import { imageMimeTypes, imageProblem, type ImageDraft, type ImageLimits, type ImageUploads } from './image-drafts'
// 传入函数时，添加按钮与缩略图条交给它放进输入框里（addClassName 调整按钮大小；没有图片时缩略图条为 null），否则都排在下方。
// uploads 是发送时各图片的上传状态，显示在对应缩略图上。
export function ImagePicker({ images, onChange, limits, disabled, uploads, addClassName, children }: { images: ImageDraft[]; onChange: (images: ImageDraft[]) => void; limits: ImageLimits; disabled?: boolean; uploads?: ImageUploads; addClassName?: string; children?: ReactNode | ((addButton: ReactNode, strip: ReactNode) => ReactNode) }) {
  const input = useRef<HTMLInputElement>(null)
  const [error, setError] = useState(''), [dragging, setDragging] = useState(false)
  const add = (files: File[]) => {
    if (disabled || !files.length) return
    const problem = imageProblem(images, files, limits); setError(problem ?? '')
    if (!problem) onChange([...images, ...files.map(file => ({ id: crypto.randomUUID(), file }))])
  }
  const paste = (event: ClipboardEvent) => { const files = Array.from(event.clipboardData.files); if (files.length && !disabled) { event.preventDefault(); add(files) } }
  const drag = (event: DragEvent) => { if (event.dataTransfer.types.includes('Files')) { event.preventDefault(); if (!disabled) setDragging(true) } }
  const addButton = <Button type="button" variant="secondary" size="icon-sm" className={cn('bg-transparent text-ink-700 hover:bg-tint-blue', addClassName)} aria-label={imageText.add} title={imageText.add} disabled={disabled} onClick={() => input.current?.click()}><PlusIcon aria-hidden="true" className="size-5" /></Button>
  // 缩略图条在输入框里横向滚动，窄屏也不撑宽页面；× 在缩略图角上，一直显示。
  const strip = images.length > 0 ? <div className="relative min-w-0">
    <ul aria-label={imageText.preview} className="flex min-w-0 gap-2 overflow-x-auto px-1 pt-1 pb-2 [scrollbar-width:thin]">{images.map((image, index) => {
      const upload = uploads?.[image.id]
      return <li key={image.id} className="relative size-[60px] shrink-0">
        <DraftImagePreview file={image.file} thumb />
        {upload === 'uploading' && <span className="pointer-events-none absolute inset-0 grid place-items-center rounded-ticket bg-white/70"><LoaderCircleIcon aria-hidden="true" className="riso-spin size-5 text-riso-blue motion-reduce:animate-none" strokeWidth={2.4} /><span className="sr-only">{imageText.uploading}</span></span>}
        {upload === 'failed' && <span className="pointer-events-none absolute inset-0 flex items-end rounded-ticket ring-2 ring-red ring-inset"><span className="w-full rounded-b-ticket bg-red py-px text-center text-[11px] leading-4 font-bold text-white">{imageText.uploadFailed}</span></span>}
        <button type="button" className="absolute top-1 right-1 grid size-5 cursor-pointer place-items-center rounded-full bg-ink/75 text-white outline-none hover:bg-ink focus-visible:shadow-focus disabled:cursor-default disabled:opacity-40 disabled:hover:bg-ink/75" disabled={disabled} aria-label={imageText.remove(index + 1)} title={imageText.remove(index + 1)} onClick={() => { onChange(images.filter(item => item.id !== image.id)); setError('') }}><XIcon aria-hidden="true" className="size-3" strokeWidth={3} /></button>
      </li>
    })}</ul>
    {Object.values(uploads ?? {}).includes('uploading') && <p role="status" className="sr-only">{imageText.loading}</p>}
  </div> : null
  return <div onPaste={paste} onDragOver={drag} onDragLeave={event => { if (!(event.relatedTarget instanceof Node) || !event.currentTarget.contains(event.relatedTarget)) setDragging(false) }} onDrop={event => { if (event.dataTransfer.types.includes('Files')) { event.preventDefault(); setDragging(false); add(Array.from(event.dataTransfer.files)) } }} className={cn('grid min-w-0 grid-cols-[minmax(0,1fr)] gap-3', dragging && 'rounded-panel ring-2 ring-riso-blue')}>
    {typeof children === 'function' ? children(addButton, strip) : <>{children}{strip}</>}
    <input ref={input} type="file" accept={imageMimeTypes.join(',')} multiple aria-label={imageText.selectFile} className="sr-only" disabled={disabled} onChange={event => { add(Array.from(event.target.files ?? [])); event.target.value = '' }} />
    {typeof children !== 'function' && <div className="flex flex-wrap items-center gap-3">{addButton}</div>}
    {error && <p role="alert" className="text-sm text-red-text">{error}</p>}
  </div>
}
