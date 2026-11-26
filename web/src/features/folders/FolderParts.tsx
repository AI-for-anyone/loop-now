import type { ComponentProps, ReactNode } from 'react'
import { DropdownMenu } from 'radix-ui'
import {
  BookOpenTextIcon, ChevronDownIcon, DownloadIcon, EllipsisIcon, FileArchiveIcon, FileBracesIcon, FileCodeIcon, FileIcon, FileImageIcon, FilePlusIcon,
  FileQuestionMarkIcon, FileSymlinkIcon, FileTerminalIcon, FileTextIcon, FolderIcon, FolderInputIcon, FolderOpenIcon, FolderPlusIcon,
  TextCursorInputIcon, Trash2Icon, UploadIcon,
} from 'lucide-react'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { extensionOf, isEntryFile, nameOf, type FolderEntry } from './folder-source'

const CODE = new Set(['go', 'ts', 'tsx', 'js', 'jsx', 'mjs', 'cjs', 'py', 'css', 'html', 'sql', 'mod', 'sum', 'xml'])

// File type icons: folders in riso blue, AGENTS.md in pink, other files in the secondary text color; symlinks and entries that can't be
// acted on each have their own marker.
export function EntryIcon({ path, type, open = false, className }: { path: string; type: FolderEntry['type']; open?: boolean; className?: string }) {
  const base = cn('size-4 shrink-0', className)
  if (type === 'symlink') return <FileSymlinkIcon aria-hidden="true" className={cn(base, 'text-ink-500')} strokeWidth={1.9} />
  if (type === 'other') return <FileQuestionMarkIcon aria-hidden="true" className={cn(base, 'text-ink-500/60')} strokeWidth={1.9} />
  if (type === 'dir') return open ? <FolderOpenIcon aria-hidden="true" className={cn(base, 'fill-tint-blue text-riso-blue')} strokeWidth={1.9} /> : <FolderIcon aria-hidden="true" className={cn(base, 'fill-tint-blue text-riso-blue')} strokeWidth={1.9} />
  if (isEntryFile(path)) return <BookOpenTextIcon aria-hidden="true" className={cn(base, 'text-pink-text')} strokeWidth={2} />
  const ext = extensionOf(nameOf(path))
  const Icon = ext === 'md' || ext === 'txt' ? FileTextIcon : ext === 'json' || ext === 'yaml' || ext === 'yml' || ext === 'toml' ? FileBracesIcon : ext === 'sh' ? FileTerminalIcon : ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp'].includes(ext) ? FileImageIcon : ['zip', 'gz', 'tgz', 'tar'].includes(ext) ? FileArchiveIcon : CODE.has(ext) ? FileCodeIcon : FileIcon
  return <Icon aria-hidden="true" className={cn(base, 'text-ink-500')} strokeWidth={1.9} />
}

// The "Entry" badge next to AGENTS.md.
export function EntryBadge({ className }: { className?: string }) {
  return (
    <span title={t.folders.entry.hint} className={cn('shrink-0 rounded-stamp border border-hairline bg-surface-subtle px-1.5 text-[11px] leading-[18px] font-medium text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-pink-2 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-pink-text', className)}>
      {t.folders.entry.badge}
    </span>
  )
}

export interface EntryActions {
  // Open an item; null is the root.
  open: (entry: FolderEntry | null) => void
  create: (dir: string, type: 'file' | 'dir') => void
  upload: (dir: string) => void
  rename: (entry: FolderEntry) => void
  move: (entry: FolderEntry) => void
  download: (entry: FolderEntry) => void
  remove: (entry: FolderEntry) => void
}

const itemClass = 'flex h-8 cursor-pointer items-center gap-2.5 rounded-[6px] px-2.5 text-[13px] font-medium text-ink outline-none select-none data-[highlighted]:bg-surface-hover data-[disabled]:cursor-default data-[disabled]:opacity-40 [[data-skin=plate]_&]:data-[highlighted]:bg-tint-blue'

function Item({ icon, children, onSelect, danger = false }: { icon: ReactNode; children: ReactNode; onSelect: () => void; danger?: boolean }) {
  return (
    <DropdownMenu.Item onSelect={onSelect} className={cn(itemClass, danger && 'text-red-text data-[highlighted]:bg-tint-red')}>
      <span aria-hidden="true" className={cn('flex size-4 items-center justify-center', danger ? 'text-red-text' : 'text-ink-500')}>{icon}</span>
      {children}
    </DropdownMenu.Item>
  )
}

// The “更多操作” (More actions) menu of a file or folder: every row in the tree and in the list has one, and the file page's title bar
// uses it too.
export function EntryMenu({ entry, actions, className, onOpenChange }: { entry: FolderEntry; actions: EntryActions; className?: string; onOpenChange?: (open: boolean) => void }) {
  const icon = 'size-3.5', path = entry.path
  return (
    <DropdownMenu.Root modal={false} onOpenChange={onOpenChange}>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          aria-label={t.folders.actions(nameOf(path))}
          onClick={(event) => event.stopPropagation()}
          onKeyDown={(event) => event.stopPropagation()}
          className={cn('inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-[6px] text-ink-500 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus data-[state=open]:bg-surface-hover data-[state=open]:text-ink [[data-skin=plate]_&]:hover:bg-white [[data-skin=plate]_&]:data-[state=open]:bg-white', className)}
        >
          <EllipsisIcon aria-hidden="true" className="size-4" strokeWidth={2.2} />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={4} onClick={(event) => event.stopPropagation()} className="z-50 min-w-44 rounded-ticket border border-hairline-strong bg-surface-elevated p-1 shadow-floating [[data-skin=plate]_&]:border-ink/15 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[3px_3px_0_rgb(24_38_94/.14)]">
          {entry.type === 'dir' && (
            <>
              <Item icon={<FilePlusIcon className={icon} />} onSelect={() => actions.create(path, 'file')}>{t.folders.newFile}</Item>
              <Item icon={<FolderPlusIcon className={icon} />} onSelect={() => actions.create(path, 'dir')}>{t.folders.newFolder}</Item>
              <Item icon={<UploadIcon className={icon} />} onSelect={() => actions.upload(path)}>{t.folders.uploadHere}</Item>
              <DropdownMenu.Separator className="mx-1 my-1 h-px bg-hairline [[data-skin=plate]_&]:bg-ink/10" />
            </>
          )}
          <Item icon={<TextCursorInputIcon className={icon} />} onSelect={() => actions.rename(entry)}>{t.folders.rename}</Item>
          <Item icon={<FolderInputIcon className={icon} />} onSelect={() => actions.move(entry)}>{t.folders.move}</Item>
          {entry.type === 'file' && <Item icon={<DownloadIcon className={icon} />} onSelect={() => actions.download(entry)}>{t.folders.download}</Item>}
          <DropdownMenu.Separator className="mx-1 my-1 h-px bg-hairline [[data-skin=plate]_&]:bg-ink/10" />
          <Item danger icon={<Trash2Icon className={icon} />} onSelect={() => actions.remove(entry)}>{t.folders.remove}</Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

// Compact 28px toolbar buttons: subtle bordered controls in mono skin, paper pills with ink outline in Plate skin.
const controlClass =
  'border border-hairline-strong bg-surface-control text-ink hover:bg-surface-hover disabled:hover:bg-surface-control [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-white [[data-skin=plate]_&]:disabled:hover:bg-paper-raised'

// Compact 28px buttons for the unified toolbar: icon plus optional text; accessible name is always present.
export function ToolButton({ icon, label, showLabel = true, onClick, disabled, active, pill = false, className, ...rest }: { icon: ReactNode; label: string; showLabel?: boolean; onClick?: () => void; disabled?: boolean; active?: boolean; pill?: boolean; className?: string } & Omit<ComponentProps<'button'>, 'onClick'>) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-ticket text-[12px] font-medium whitespace-nowrap text-ink-700 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:hover:bg-tint-blue [&_svg]:size-3.5 [&_svg]:shrink-0',
        showLabel ? 'px-2.5' : 'w-7 justify-center',
        pill && controlClass,
        active && 'bg-surface-active text-ink [[data-skin=plate]_&]:bg-tint-blue',
        className,
      )}
      {...rest}
    >
      {icon}
      {showLabel && <span>{label}</span>}
    </button>
  )
}

// Toolbar Upload menu: upload files or a whole folder.
export function UploadMenu({ onFiles, onFolder, pill = false, className }: { onFiles: () => void; onFolder: () => void; pill?: boolean; className?: string }) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          aria-label={t.folders.upload}
          title={t.folders.upload}
          className={cn(
            'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1 rounded-ticket border border-hairline-strong bg-surface-control px-2.5 text-[12px] font-medium text-ink hover:bg-surface-hover focus-visible:shadow-focus data-[state=open]:bg-surface-hover [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-ink/20 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:hover:bg-tint-blue [[data-skin=plate]_&]:data-[state=open]:bg-tint-blue',
            pill && controlClass,
            className,
          )}
        >
          <UploadIcon aria-hidden="true" className="size-3.5 shrink-0" />
          <span>{t.folders.upload}</span>
          <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0 opacity-60" />
        </button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={4} className="z-50 min-w-40 rounded-ticket border border-hairline-strong bg-surface-elevated p-1 shadow-floating [[data-skin=plate]_&]:border-ink/15 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-[3px_3px_0_rgb(24_38_94/.14)]">
          <Item icon={<FilePlusIcon className="size-3.5" />} onSelect={onFiles}>{t.folders.uploadFiles}</Item>
          <Item icon={<FolderPlusIcon className="size-3.5" />} onSelect={onFolder}>{t.folders.uploadFolder}</Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
