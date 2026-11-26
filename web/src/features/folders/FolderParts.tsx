import type { ComponentProps, ReactNode } from 'react'
import { DropdownMenu } from 'radix-ui'
import {
  BookOpenTextIcon, DownloadIcon, EllipsisIcon, FileArchiveIcon, FileBracesIcon, FileCodeIcon, FileIcon, FileImageIcon, FilePlusIcon,
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

// Icons in the file list and the path bar (redesign): a folder is a yellow folder block with a navy outline and an offset shadow, a file is
// a white sheet of paper; symlinks and other entries keep the line icons.
export function FolderBlock({ className }: { className?: string }) {
  return <span aria-hidden="true" data-folder-icon className={cn('block h-[22px] w-[26px] shrink-0 rounded-[3px_6px_6px_6px] border border-hairline-strong bg-surface-active [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]', className)} />
}

export function ListIcon({ path, type }: { path: string; type: FolderEntry['type'] }) {
  if (type === 'dir') return <FolderBlock />
  if (type === 'file') return <span aria-hidden="true" data-file-icon className="mx-[3px] block h-6 w-5 shrink-0 rounded-[3px_8px_3px_3px] border border-hairline-strong bg-surface-control [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white" />
  return <EntryIcon path={path} type={type} className="mx-[5px] size-4" />
}

// The “入口” (Entry) badge next to AGENTS.md.
export function EntryBadge({ className }: { className?: string }) {
  return (
    <span title={t.folders.entry.hint} className={cn('shrink-0 rounded-stamp bg-tint-pink-2 px-1.5 text-[11px] leading-[18px] font-black text-pink-text', className)}>
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

const itemClass = 'flex h-8 cursor-pointer items-center gap-2.5 rounded-[6px] px-2.5 text-[13px] font-medium text-ink outline-none select-none data-[highlighted]:bg-tint-blue data-[disabled]:cursor-default data-[disabled]:opacity-40'

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
              <DropdownMenu.Separator className="mx-1 my-1 h-px bg-ink/10" />
            </>
          )}
          <Item icon={<TextCursorInputIcon className={icon} />} onSelect={() => actions.rename(entry)}>{t.folders.rename}</Item>
          <Item icon={<FolderInputIcon className={icon} />} onSelect={() => actions.move(entry)}>{t.folders.move}</Item>
          {entry.type === 'file' && <Item icon={<DownloadIcon className={icon} />} onSelect={() => actions.download(entry)}>{t.folders.download}</Item>}
          <DropdownMenu.Separator className="mx-1 my-1 h-px bg-ink/10" />
          <Item danger icon={<Trash2Icon className={icon} />} onSelect={() => actions.remove(entry)}>{t.folders.remove}</Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}

// Buttons on the toolbar at the top of the sheet: crisp controls in mono skin; pills with paper background and 2px navy outline in Plate skin.
const pillClass = 'h-[34px] rounded-ticket border border-hairline-strong bg-surface-control text-[13px] font-semibold hover:bg-surface-hover disabled:hover:bg-surface-control [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:hover:bg-white [[data-skin=plate]_&]:disabled:hover:bg-paper-raised'

// Small buttons for the toolbar and title bar: 28 high, an icon plus optional text; with only the icon, the text serves as the accessible
// name and the hover tooltip.
export function ToolButton({ icon, label, showLabel = true, onClick, disabled, active, pill = false, className, ...rest }: { icon: ReactNode; label: string; showLabel?: boolean; onClick?: () => void; disabled?: boolean; active?: boolean; pill?: boolean; className?: string } & Omit<ComponentProps<'button'>, 'onClick'>) {
  return (
    <button
      type="button"
      aria-label={showLabel ? undefined : label}
      title={label}
      onClick={onClick}
      disabled={disabled}
      className={cn(
        'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full text-[12.5px] font-bold whitespace-nowrap text-ink hover:bg-tint-blue focus-visible:shadow-focus disabled:cursor-default disabled:opacity-40 disabled:hover:bg-transparent [&_svg]:size-[15px] [&_svg]:shrink-0',
        showLabel ? 'px-2.5' : 'w-7 justify-center',
        pill && pillClass,
        pill && (showLabel ? 'px-3.5' : 'w-[34px]'),
        active && 'bg-tint-blue',
        className,
      )}
      {...rest}
    >
      {icon}
      {showLabel && <span>{label}</span>}
    </button>
  )
}

// The toolbar's “上传” (Upload) menu: upload files or a whole folder.
export function UploadMenu({ onFiles, onFolder, pill = false, className }: { onFiles: () => void; onFolder: () => void; pill?: boolean; className?: string }) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild>
        <button type="button" title={t.folders.upload} className={cn('inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border border-hairline-strong bg-surface-control px-2.5 text-[12.5px] font-semibold text-ink hover:bg-surface-hover focus-visible:shadow-focus data-[state=open]:bg-surface-hover [[data-skin=plate]_&]:border-ink/20 [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:hover:bg-tint-blue [[data-skin=plate]_&]:data-[state=open]:bg-tint-blue [&_svg]:size-[15px]', pill && cn(pillClass, 'px-3.5'), className)}>
          <UploadIcon aria-hidden="true" />
          <span>{t.folders.upload}</span>
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
