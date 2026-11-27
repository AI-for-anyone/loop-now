import { useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { ArrowLeftIcon, PlusIcon, SearchIcon, XIcon } from 'lucide-react'
import { Link, NavLink, useNavigate, useOutlet, useParams } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SubNav } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { Sheet as PaperSheet } from '@/components/Sheet'
import type { Schemas } from '@/api/client'
import {
  useCreateSkillFromArchive,
  useCreateSkillFromMarkdown,
  useDeleteSkill,
  useReplaceSkill,
  useSkill,
  useSkills,
  useUpdateSkillMarkdown,
} from '@/api/queries'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { SkillUpload } from './SkillUpload'
import { useSkillMdEditor } from './use-skill-md-editor'

const editorClass =
  'h-full min-h-0 w-full flex-1 resize-none rounded-none border-0 bg-surface-canvas px-5 py-4 font-mono text-[13px] leading-[1.7] field-sizing-fixed [field-sizing:fixed] focus-visible:shadow-none'

// Master-detail Skills Library workspace: left searchable rail of templates and inline right workspace pane for creating or editing
// SKILL.md and inspecting files, driven directly by /skills, /skills/new and /skills/:skillId.
export function SkillsPage() {
  const skills = useSkills()
  const navigate = useNavigate()
  const outlet = useOutlet()
  const create = useCreateSkillFromArchive()
  const [query, setQuery] = useState('')
  const railScrollRef = useRef<HTMLDivElement>(null)
  const searchId = useId()

  const list = useMemo(() => skills.data?.skills ?? [], [skills.data?.skills])
  const empty = skills.isSuccess && list.length === 0
  const hasDetail = outlet !== null
  const normalizedQuery = query.trim().toLowerCase()
  const filtered = useMemo(() => {
    if (!normalizedQuery) return list
    return list.filter(
      (skill) => skill.name.toLowerCase().includes(normalizedQuery) || skill.description.toLowerCase().includes(normalizedQuery),
    )
  }, [list, normalizedQuery])

  const headerNewLink = (
    <Button size="xs" variant="interrupt" asChild>
      <Link to="/skills/new">
        <PlusIcon aria-hidden="true" />
        {t.skills.create}
      </Link>
    </Button>
  )

  return (
    <SkillUpload
      size="xs"
      secondarySize="sm"
      onArchive={async (archive) => navigate(`/skills/${(await create.mutateAsync(archive)).id}`)}
    >
      {({ actions, secondaryActions, feedback }) => (
        <PageFrame>
          <PageHeader
            crumbs={[{ label: t.shell.global }]}
            title={t.skills.title}
            actions={
              <>
                {actions}
                {headerNewLink}
              </>
            }
          >
            {skills.isSuccess && <span>{t.skills.count(list.length)}</span>}
          </PageHeader>
          {feedback && (
            <div className="shrink-0 border-b border-hairline bg-surface-canvas px-4 py-2.5 sm:px-6">
              {feedback}
            </div>
          )}
          <PaperSheet className="flex-1 flex-row">
            {empty && !hasDetail ? (
              <div className="flex min-h-0 flex-1 items-center justify-center overflow-y-auto px-4">
                <EmptyLibrary>
                  {secondaryActions}
                  <Button size="sm" variant="interrupt" asChild>
                    <Link to="/skills/new">
                      <PlusIcon aria-hidden="true" />
                      {t.skills.create}
                    </Link>
                  </Button>
                </EmptyLibrary>
              </div>
            ) : (
              <>
                {!empty && (
                  <aside
                    aria-label={t.skills.title}
                    className={cn(
                      'flex min-h-0 w-full shrink-0 flex-col border-hairline nav:w-[272px] nav:border-r lg:w-[296px]',
                      hasDetail && 'max-nav:hidden',
                    )}
                  >
                    <div className="sticky top-0 z-10 flex shrink-0 items-center gap-1.5 border-b border-hairline bg-surface-canvas px-3 py-2">
                      <div className="relative min-w-0 flex-1">
                        <SearchIcon aria-hidden="true" className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-ink-500" />
                        <Input
                          id={searchId}
                          type="search"
                          value={query}
                          onChange={(e) => setQuery(e.target.value)}
                          placeholder={t.skills.searchPlaceholder}
                          aria-label={t.skills.searchLabel}
                          className="h-8 pr-7 pl-8 text-[12.5px]"
                        />
                        {query && (
                          <button
                            type="button"
                            onClick={() => setQuery('')}
                            aria-label={t.common.close}
                            className="absolute top-1/2 right-1.5 inline-flex size-5 -translate-y-1/2 items-center justify-center rounded-stamp text-ink-500 hover:bg-surface-hover hover:text-ink"
                          >
                            <XIcon className="size-3" />
                          </button>
                        )}
                      </div>
                      {normalizedQuery && (
                        <Badge variant="chip" tone="neutral" data-filtered-count className="shrink-0 font-mono text-[11px] tabular-nums">
                          {filtered.length}/{list.length}
                        </Badge>
                      )}
                    </div>
                    <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
                      <div ref={railScrollRef} className="min-h-0 flex-1 overflow-y-auto">
                        {skills.isError && (
                          <div className="p-3">
                            <ErrorAlert error={skills.error} />
                          </div>
                        )}
                        {skills.isPending && <p className="px-3 py-4 text-[13px] text-ink-500">{t.common.loading}</p>}
                        {skills.isSuccess && filtered.length === 0 && (
                          <p className="px-3 py-6 text-center text-[13px] text-ink-500">{t.skills.noMatch}</p>
                        )}
                        {filtered.length > 0 && (
                          <ul
                            aria-label={t.skills.title}
                            className="flex flex-col divide-y divide-hairline/70"
                          >
                            {filtered.map((skill) => (
                              <li key={skill.id} className="min-w-0">
                                <TemplateRow skill={skill} />
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                      <ScrollRuler targetRef={railScrollRef} label={t.skills.title} />
                    </div>
                  </aside>
                )}
                <div className={cn('relative flex min-h-0 min-w-0 flex-1 flex-col', !hasDetail && 'max-nav:hidden')}>
                  {hasDetail ? (
                    outlet
                  ) : (
                    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 py-8 text-center">
                      <p className="max-w-[360px] text-[13.5px] leading-relaxed text-ink-500">{t.skills.selectHint}</p>
                      <div className="flex flex-wrap justify-center gap-2">
                        {actions}
                        {headerNewLink}
                      </div>
                    </div>
                  )}
                </div>
              </>
            )}
          </PaperSheet>
        </PageFrame>
      )}
    </SkillUpload>
  )
}

// Dense 2-line template row in the master rail (~52px tall): monospace name + file count on row 1, truncated description on row 2.
function TemplateRow({ skill }: { skill: Schemas['SkillTemplateSummary'] }) {
  return (
    <NavLink
      to={`/skills/${skill.id}`}
      className={({ isActive }) =>
        cn(
          'flex min-h-[52px] w-full flex-col justify-center gap-0.5 px-3 py-2 text-ink no-underline outline-none transition-colors focus-visible:shadow-focus',
          isActive
            ? 'bg-surface-active text-ink'
            : 'hover:bg-surface-hover',
        )
      }
    >
      <span className="flex w-full min-w-0 items-center gap-2">
        <span className="min-w-0 flex-1 truncate font-mono text-[13px] font-semibold">{skill.name}</span>
        <span className="shrink-0 font-mono text-[11px] font-medium text-ink-500 tabular-nums">
          {t.skills.files(skill.fileCount)}
        </span>
      </span>
      <span className="truncate text-[12px] leading-[1.45] text-ink-500">{skill.description}</span>
    </NavLink>
  )
}

// Empty state: compact centered prompt.
function EmptyLibrary({ children }: { children: ReactNode }) {
  return (
    <div data-empty-library className="flex flex-col items-center gap-5 px-6 py-8 text-center">
      <p className="riso-marker font-display-cn text-[18px] leading-snug font-semibold tracking-tight">
        {t.skills.empty}
      </p>
      <div className="flex flex-wrap justify-center gap-2.5">{children}</div>
    </div>
  )
}

function MobileBackLink() {
  return (
    <Link
      to="/skills"
      aria-label={t.skills.backToList}
      className="inline-flex size-7 shrink-0 items-center justify-center rounded-ticket border border-hairline text-ink-700 hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus nav:hidden"
    >
      <ArrowLeftIcon className="size-3.5" aria-hidden="true" />
    </Link>
  )
}

// Inline create pane (/skills/new): compact toolbar + full-height flex-filling SKILL.md editor prefilled with the skeleton.
// Exported as NewSkillSheet so routes.tsx stays valid without route changes.
export function NewSkillSheet() {
  const navigate = useNavigate()
  const create = useCreateSkillFromMarkdown()
  const editorId = useId()
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const close = () => void navigate('/skills')
  const editor = useSkillMdEditor(
    t.skills.skeleton,
    async (content) => {
      const skill = await create.mutateAsync(content)
      void navigate(`/skills/${skill.id}`)
    },
    { requireDirty: false },
  )

  return (
    <section aria-label={t.skills.newTitle} className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-hairline px-5 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          <MobileBackLink />
          <h2 className="font-display-cn text-[15px] leading-snug font-semibold tracking-tight">
            {t.skills.newTitle}
          </h2>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button size="xs" variant="secondary" onClick={close}>
            {t.common.cancel}
          </Button>
          <Button size="xs" disabled={!editor.canSave} onClick={() => void editor.save()}>
            {t.skills.submitCreate}
          </Button>
        </div>
      </div>
      {editor.error !== null && (
        <div className="shrink-0 border-b border-hairline px-5 py-2.5">
          <ErrorAlert error={editor.error} />
        </div>
      )}
      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
        <Textarea
          ref={editorRef}
          id={editorId}
          aria-label={t.skills.skillMd}
          spellCheck={false}
          className={editorClass}
          value={editor.content}
          onChange={(e) => editor.setContent(e.target.value)}
          onKeyDown={editor.onKeyDown}
        />
        <ScrollRuler targetRef={editorRef} label={t.skills.skillMd} />
      </div>
    </section>
  )
}

// Inline detail pane (/skills/:skillId): compact toolbar, flush SKILL.md / Files SubNav, and full-bleed flex-filling editor.
// Exported as SkillDetailSheet so routes.tsx stays valid without route changes.
export function SkillDetailSheet() {
  const { skillId = '' } = useParams()
  const skill = useSkill(skillId)
  const data = skill.data
  const notFound = isNotFound(skill.error)

  if (data && !notFound) {
    return (
      <section aria-label={data.name} className="flex min-h-0 flex-1 flex-col">
        <SkillDetailBody key={skillId} skillId={skillId} data={data} refreshError={skill.isError ? skill.error : null} />
      </section>
    )
  }

  return (
    <section aria-label={t.skills.detailTitle} className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center gap-2 border-b border-hairline px-5 py-2.5">
        <MobileBackLink />
        <h2 className="font-display-cn text-[15px] leading-snug font-semibold tracking-tight">
          {t.skills.detailTitle}
        </h2>
      </div>
      <div className="flex min-h-0 flex-1 flex-col p-5">
        {notFound ? (
          <p className="text-[14px] text-ink-500">{t.skills.notFound}</p>
        ) : skill.isError ? (
          <ErrorAlert error={skill.error} />
        ) : (
          <p className="text-[14px] text-ink-500">{t.common.loading}</p>
        )}
      </div>
    </section>
  )
}

type DetailTab = 'skillMd' | 'files'

function SkillDetailBody({ skillId, data, refreshError }: { skillId: string; data: Schemas['SkillTemplate']; refreshError: unknown }) {
  const navigate = useNavigate()
  const update = useUpdateSkillMarkdown(skillId)
  const replace = useReplaceSkill(skillId)
  const remove = useDeleteSkill(skillId)
  const [tab, setTab] = useState<DetailTab>('skillMd')
  const [replacement, setReplacement] = useState<Blob | null>(null)
  const [deleting, setDeleting] = useState(false)
  const editorId = useId()
  const editorRef = useRef<HTMLTextAreaElement>(null)
  const filesRef = useRef<HTMLDivElement>(null)
  // Reset when server content changes (updatedAt changes); a failed background refresh keeps unsaved edits.
  const editor = useSkillMdEditor(data.skillMd, (content) => update.mutateAsync(content), {
    resetKey: `${skillId}:${data.updatedAt}`,
    requireDirty: true,
  })

  return (
    <SkillUpload size="xs" onArchive={async (archive) => setReplacement(archive)}>
      {({ actions, secondaryActions, feedback }) => (
        <>
          <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline px-5 py-2.5">
            <div className="flex min-w-0 flex-1 items-start gap-2.5">
              <MobileBackLink />
              <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <div className="flex min-w-0 flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
                  <h2 className="truncate font-mono text-[14.5px] font-semibold text-ink">{data.name}</h2>
                  <span className="shrink-0 text-[12px] font-medium text-ink-500">{t.skills.updated(data.updatedAt)}</span>
                </div>
                <p className="truncate text-[12.5px] text-ink-700">{data.description}</p>
              </div>
            </div>
            <div className="flex shrink-0 flex-wrap items-center gap-2">
              {actions}
              <Button size="xs" variant="danger-outline" onClick={() => setDeleting(true)}>
                {t.common.delete}
              </Button>
              <Button size="xs" disabled={!editor.canSave} title={`${t.common.save} (⌘S)`} onClick={() => void editor.save()}>
                {editor.isDirty && (
                  <span
                    aria-label={t.skills.unsaved}
                    data-dirty-dot
                    className="size-1.5 rounded-full bg-current"
                  />
                )}
                {t.common.save}
              </Button>
            </div>
          </div>
          <SubNav
            aria-label={data.name}
            value={tab}
            onChange={setTab}
            items={[
              {
                key: 'skillMd' as const,
                label: t.skills.skillMd,
                badge: editor.isDirty ? <span aria-hidden="true" className="size-1.5 rounded-full bg-ink" /> : undefined,
              },
              {
                key: 'files' as const,
                label: t.skills.fileList,
                count: data.files.length,
              },
            ]}
          />
          {(refreshError !== null || feedback !== null || editor.error !== null) && (
            <div className="flex shrink-0 flex-col gap-2 border-b border-hairline px-5 py-2.5">
              <ErrorAlert error={refreshError} />
              {feedback}
              <ErrorAlert error={editor.error} />
            </div>
          )}
          {tab === 'skillMd' ? (
            <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
              <Textarea
                ref={editorRef}
                id={editorId}
                aria-label={t.skills.skillMd}
                spellCheck={false}
                className={editorClass}
                value={editor.content}
                onChange={(e) => editor.setContent(e.target.value)}
                onKeyDown={editor.onKeyDown}
              />
              <ScrollRuler targetRef={editorRef} label={t.skills.skillMd} />
            </div>
          ) : (
            <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
              <div ref={filesRef} className="min-h-0 flex-1 overflow-y-auto px-5 py-4">
                <table aria-label={t.skills.fileList} className="w-full border-collapse text-[13px]">
                  <thead>
                    <tr className="border-b border-hairline-strong text-left text-[11.5px] font-medium text-ink-500">
                      <th scope="col" className="py-1.5 pr-4 font-medium">
                        {t.skills.columns.path}
                      </th>
                      <th scope="col" className="w-28 py-1.5 text-right font-medium">
                        {t.skills.columns.size}
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.files.map((file) => (
                      <tr
                        key={file.path}
                        className="border-b border-hairline"
                      >
                        <td className="py-1.5 pr-4 font-mono text-[12.5px] [overflow-wrap:anywhere]">{file.path}</td>
                        <td className="py-1.5 text-right font-mono text-[12px] text-ink-500 tabular-nums">{t.common.bytes(file.size)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                <div className="mt-4 flex flex-wrap items-center justify-between gap-3 rounded-card border border-hairline bg-surface-subtle px-4 py-3">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="text-[13px] font-semibold text-ink">{t.skills.replace}</span>
                    <span className="text-[12px] text-ink-500">{t.skills.replaceHint}</span>
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-2">
                    {secondaryActions}
                  </div>
                </div>
              </div>
              <ScrollRuler targetRef={filesRef} label={t.skills.fileList} />
            </div>
          )}
          <ConfirmDialog
            open={replacement !== null}
            onOpenChange={(open) => !open && setReplacement(null)}
            title={t.skills.replaceTitle(data.name)}
            body={t.skills.replaceBody}
            confirmLabel={t.skills.replace}
            onConfirm={() => (replacement ? replace.mutateAsync(replacement) : undefined)}
          />
          <ConfirmDialog
            open={deleting}
            onOpenChange={setDeleting}
            title={t.skills.deleteTitle(data.name)}
            body={t.skills.deleteBody}
            confirmLabel={t.common.delete}
            destructive
            onConfirm={async () => {
              await remove.mutateAsync()
              void navigate('/skills')
            }}
          />
        </>
      )}
    </SkillUpload>
  )
}
