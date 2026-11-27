import { useState, type FocusEvent } from 'react'
import { Link as RouterLink, useInRouterContext } from 'react-router'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { nameOf } from '@/features/folders/folder-source'
import { t } from '@/i18n'
import { fileCardAppearance, formatFileCardSize, type FileCardGlyph } from './file-cards'
import { useLocalEntryStatuses, type LocalProbeStore } from './local-files'
import type { ReplyFolderContext } from './local-images'
import type { FileCardCandidate } from './rehype-local-files'

export const MAX_VISIBLE_FILE_CARDS = 6

function FileCardGlyphIcon({ glyph }: { glyph: FileCardGlyph }) {
  switch (glyph) {
    case 'code':
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="m8 7-5 5 5 5M16 7l5 5-5 5" />
        </svg>
      )
    case 'pdf':
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
          <path d="M14 3v5h5" />
          <path d="M9 15h1.5a1.5 1.5 0 0 0 0-3H9v5" />
        </svg>
      )
    case 'doc':
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
          <path d="M14 3v5h5M9 13h6M9 17h4" />
        </svg>
      )
    case 'table':
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 10h18M3 15h18M9 4v16" />
        </svg>
      )
    case 'file':
      return (
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
          <path d="M14 3v5h5" />
        </svg>
      )
  }
}

function FolderActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
    </svg>
  )
}

function NewTabActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M14 4h6v6M20 4l-9 9M18 14v4a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h4" />
    </svg>
  )
}

export interface ResolvedFileCard {
  relativePath: string
  state: 'ready' | 'missing'
  size?: number
}

export function FileCard({
  card,
  folder,
}: {
  card: ResolvedFileCard
  folder: ReplyFolderContext
}) {
  const inRouter = useInRouterContext()
  const [focused, setFocused] = useState(false)
  const name = nameOf(card.relativePath)
  const appearance = fileCardAppearance(card.relativePath)
  const copy = t.folders.fileCards

  if (card.state === 'missing') {
    return (
      <div
        className="md-file-card"
        data-md-file-card={card.relativePath}
        data-md-file-card-state="missing"
        data-md-file-card-kind={appearance.kind}
        title={card.relativePath}
      >
        <span className="md-file-card-icon" aria-hidden="true">
          <FileCardGlyphIcon glyph={appearance.glyph} />
        </span>
        <span className="md-file-card-meta">
          <span className="md-file-card-name">{name}</span>
          <span className="md-file-card-sub">{copy.missing}</span>
        </span>
      </div>
    )
  }

  const rawHref = folder.rawURL?.(card.relativePath)
  if (!rawHref) return null
  const folderHref = folder.folderLink?.(card.relativePath)
  const kindLabel = copy.kinds[appearance.kind]
  const subline = card.size !== undefined ? `${kindLabel} · ${formatFileCardSize(card.size)}` : kindLabel

  const onBlur = (event: FocusEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setFocused(false)
    }
  }

  return (
    <div
      className="md-file-card"
      data-md-file-card={card.relativePath}
      data-md-file-card-state="ready"
      data-md-file-card-kind={appearance.kind}
      data-focused={focused ? 'true' : undefined}
      onFocus={() => setFocused(true)}
      onBlur={onBlur}
    >
      <a
        href={rawHref}
        target="_blank"
        rel="noopener noreferrer"
        className="md-file-card-main"
        aria-label={copy.openLabel(name, subline)}
        title={card.relativePath}
      >
        <span className="md-file-card-icon" style={{ backgroundColor: appearance.color }} aria-hidden="true">
          <FileCardGlyphIcon glyph={appearance.glyph} />
        </span>
        <span className="md-file-card-meta">
          <span className="md-file-card-name">{name}</span>
          <span className="md-file-card-sub">{subline}</span>
        </span>
      </a>
      <div className="md-file-card-actions" data-md-file-card-actions={focused ? 'visible' : 'hover'}>
        {folderHref && (
          <Tooltip>
            <TooltipTrigger asChild>
              {inRouter ? (
                <RouterLink
                  to={folderHref}
                  className="md-file-card-action"
                  aria-label={copy.showInFolder}
                  data-md-file-card-folder={card.relativePath}
                >
                  <FolderActionIcon />
                </RouterLink>
              ) : (
                <a
                  href={folderHref}
                  className="md-file-card-action"
                  aria-label={copy.showInFolder}
                  data-md-file-card-folder={card.relativePath}
                >
                  <FolderActionIcon />
                </a>
              )}
            </TooltipTrigger>
            <TooltipContent side="bottom" sideOffset={6}>
              {copy.showInFolder}
            </TooltipContent>
          </Tooltip>
        )}
        <Tooltip>
          <TooltipTrigger asChild>
            <a
              href={rawHref}
              target="_blank"
              rel="noopener noreferrer"
              className="md-file-card-action"
              aria-label={copy.openInNewTab}
              data-md-file-card-open={card.relativePath}
            >
              <NewTabActionIcon />
            </a>
          </TooltipTrigger>
          <TooltipContent side="bottom" sideOffset={6}>
            {copy.openInNewTab}
          </TooltipContent>
        </Tooltip>
      </div>
    </div>
  )
}

export function FileCardList({
  cards,
  folder,
  inline = false,
}: {
  cards: readonly ResolvedFileCard[]
  folder: ReplyFolderContext
  inline?: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  if (cards.length === 0) return null

  const hiddenCount = expanded ? 0 : Math.max(0, cards.length - MAX_VISIBLE_FILE_CARDS)
  const visible = hiddenCount > 0 ? cards.slice(0, MAX_VISIBLE_FILE_CARDS) : cards

  return (
    <TooltipProvider delayDuration={0}>
      <div className="md-file-cards-wrap" data-md-file-cards-strip="" data-inline={inline ? '' : undefined}>
        <div className="md-file-cards">
          {visible.map((card) => (
            <FileCard key={card.relativePath} card={card} folder={folder} />
          ))}
        </div>
        {hiddenCount > 0 && (
          <button
            type="button"
            className="md-file-cards-more"
            aria-expanded={false}
            onClick={() => setExpanded(true)}
          >
            {t.folders.fileCards.more(hiddenCount)}
          </button>
        )}
      </div>
    </TooltipProvider>
  )
}

export function LocalFileCardStrip({
  candidates,
  folder,
  streaming,
  store,
  inline = false,
}: {
  candidates: readonly FileCardCandidate[]
  folder: ReplyFolderContext | undefined
  streaming: boolean
  store: LocalProbeStore | null
  inline?: boolean
}) {
  const paths = candidates.map((item) => item.relativePath)
  const statuses = useLocalEntryStatuses(folder, paths, streaming, store)
  if (!folder || streaming || candidates.length === 0) return null

  const resolved: ResolvedFileCard[] = []
  for (const item of candidates) {
    const status = statuses[item.relativePath]
    if (!status || status === 'loading') continue
    if (status.kind === 'file' && folder.rawURL) {
      resolved.push({ relativePath: item.relativePath, state: 'ready', size: status.size })
    } else if (status.kind === 'missing' && item.certain) {
      resolved.push({ relativePath: item.relativePath, state: 'missing' })
    }
  }

  return <FileCardList cards={resolved} folder={folder} inline={inline} />
}
