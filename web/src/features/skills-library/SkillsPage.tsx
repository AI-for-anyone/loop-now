import { useId, useState, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import { Link, Outlet, useNavigate, useParams } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useCreateSkillFromArchive, useCreateSkillFromMarkdown, useDeleteSkill, useReplaceSkill, useSkill, useSkills, useUpdateSkillMarkdown } from '@/api/queries'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { SkillUpload } from './SkillUpload'
import { useSkillMdEditor } from './use-skill-md-editor'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet as PaperSheet } from '@/components/Sheet'

// Upload folder and Upload zip: crisp secondary controls in mono skin; white pills with a navy outline and shadow in Plate skin.
const uploadButtonClass = '[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[3px_3px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-paper-raised'

// The URL decides whether a drawer is open: /skills/:skillId opens the detail drawer, /skills/new opens the create-online
// drawer, and closing goes back to /skills.
// Drawers render as child routes on top of the grid, so the grid still shows when either URL is refreshed or opened directly.
// Redesign: the header is the shared page title (next to it the template count, plus the upload and create entries when there
// are templates); the templates and the empty state are laid out on one sheet.
export function SkillsPage() {
  const skills = useSkills()
  const navigate = useNavigate()
  const create = useCreateSkillFromArchive()
  const { skillId } = useParams()
  const list = skills.data?.skills ?? []
  const empty = skills.isSuccess && list.length === 0
  const newLink = (
    <Button size={empty ? 'lg' : 'xs'} variant="interrupt" className="[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink" asChild>
      <Link to="/skills/new">
        <PlusIcon aria-hidden="true" />
        {t.skills.create}
      </Link>
    </Button>
  )
  return (
    <SkillUpload
      size={empty ? 'lg' : 'sm'}
      buttonClassName={uploadButtonClass}
      onArchive={async (archive) => navigate(`/skills/${(await create.mutateAsync(archive)).id}`)}
    >
      {({ actions, feedback }) => (
        <PageFrame>
          <PageHeader crumbs={[{ label: t.shell.global }]} title={t.skills.title}>
            {skills.isSuccess && <span>{t.skills.count(list.length)}</span>}
            {/* With an empty library, the upload and create entries only appear in the empty state, not again in the header. */}
            {!empty && skills.isSuccess && (
              <>
                {actions}
                {newLink}
              </>
            )}
          </PageHeader>
          <PaperSheet className="mt-2 flex-1">
            {/* The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text)
                scroll with the content instead of stretching the whole page based on their unscrolled position. */}
            <div className="relative min-h-0 flex-1 overflow-y-auto">
              <div className={cn('flex min-h-full flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]', empty && 'justify-center')}>
                {feedback}
                {empty && (
                  <EmptyLibrary>
                    {actions}
                    {newLink}
                  </EmptyLibrary>
                )}
                {skills.isError && <ErrorAlert error={skills.error} />}
                {skills.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
                {list.length > 0 && (
                  <ul className="grid grid-cols-[repeat(auto-fill,minmax(min(300px,100%),1fr))] gap-[18px]" aria-label={t.skills.title}>
                    {list.map((skill) => (
                      <li key={skill.id} className="flex">
                        <TemplateCard skill={skill} selected={skill.id === skillId} />
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          </PaperSheet>
          <Outlet />
        </PageFrame>
      )}
    </SkillUpload>
  )
}

// Template card: name (monospace), file count, description and update time. The whole card is a link that opens the detail drawer.
// Redesign: a white card with a 2px navy outline and a solid navy offset shadow; light yellow on hover, and the one currently
// open is yellow with a fluorescent pink shadow (the selected state is always yellow).
function TemplateCard({ skill, selected }: { skill: Schemas['SkillTemplateSummary']; selected: boolean }) {
  return (
    <Link
      to={`/skills/${skill.id}`}
      aria-current={selected ? 'true' : undefined}
      className="flex w-full flex-col gap-3 rounded-card border border-hairline-strong bg-surface-control p-5 text-ink no-underline shadow-surface outline-none hover:bg-surface-hover focus-visible:shadow-focus aria-[current=true]:border-ink aria-[current=true]:bg-surface-active [[data-skin=plate]_&]:mr-1 [[data-skin=plate]_&]:mb-1 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)] [[data-skin=plate]_&]:hover:bg-tint-yellow [[data-skin=plate]_&]:aria-[current=true]:bg-riso-yellow [[data-skin=plate]_&]:aria-[current=true]:shadow-print-pink"
    >
      <span className="flex w-full items-center gap-3">
        <span className="min-w-0 flex-1 truncate font-mono text-[15.5px] font-bold">{skill.name}</span>
        <Badge variant="chip" tone="neutral">
          {t.skills.files(skill.fileCount)}
        </Badge>
      </span>
      <span className="line-clamp-2 min-h-[46px] text-[14px] leading-[1.65] text-ink-700">{skill.description}</span>
      <span className="text-[12.5px] font-bold text-ink-500">{t.skills.updated(skill.updatedAt)}</span>
    </Link>
  )
}

// Empty state (redesign): a patch of halftone dots, two overprinted cards (riso blue, fluorescent pink) and a yellow circle,
// with a yellow highlighter-mark title and three entries below.
function EmptyLibrary({ children }: { children: ReactNode }) {
  return (
    <div data-empty-library className="flex flex-col items-center gap-[22px] px-6 py-10 text-center">
      <span aria-hidden="true" data-empty-art className="relative block h-[190px] w-[290px] max-w-full">
        <span className="absolute top-[52px] left-0 h-[112px] w-[96px] bg-[radial-gradient(var(--ink)_1.5px,transparent_1.9px)] bg-size-[9px_9px] opacity-55" />
        <span className="absolute top-[36px] left-[66px] h-[140px] w-[106px] rotate-[-8deg] rounded-[12px] bg-riso-blue bg-[radial-gradient(rgb(24_38_94/.45)_1.3px,transparent_1.7px)] bg-size-[8px_8px]" />
        <span className="absolute top-[18px] left-[124px] h-[140px] w-[106px] rotate-[6deg] rounded-[12px] bg-riso-pink mix-blend-multiply" />
        <span className="absolute top-0 left-[194px] size-[84px] rounded-full bg-riso-yellow mix-blend-multiply" />
      </span>
      <p className="riso-marker px-1.5 font-display-cn text-[34px] leading-[1.2]">{t.skills.empty}</p>
      <div className="flex flex-wrap justify-center gap-3">{children}</div>
    </div>
  )
}

// Drawer footer: outside the body's scroll area, always visible.
const editorClass = 'px-4 py-3.5 font-mono text-[13px] leading-[1.7]'
const sectionTitleClass = 'font-display-cn text-[24px] leading-[1.2] font-normal'

export function NewSkillSheet() {
  const navigate = useNavigate()
  const create = useCreateSkillFromMarkdown()
  const editorId = useId()
  const close = () => void navigate('/skills')
  const editor = useSkillMdEditor(t.skills.skeleton, async (content) => {
    const skill = await create.mutateAsync(content)
    void navigate(`/skills/${skill.id}`)
  })
  return (
    <Sheet open onOpenChange={(open) => !open && close()}>
      <SheetContent aria-describedby={undefined}>
        <SheetHeader className="items-center">
          <SheetTitle className="font-display-cn text-[32px] leading-[1.2] font-normal">{t.skills.newTitle}</SheetTitle>
        </SheetHeader>
        <SheetBody className="gap-2.5">
          <label htmlFor={editorId} className={sectionTitleClass}>
            {t.skills.skillMd}
          </label>
          <Textarea id={editorId} className={cn(editorClass, 'min-h-[520px]')} value={editor.content} onChange={(e) => editor.setContent(e.target.value)} />
          <ErrorAlert error={editor.error} />
        </SheetBody>
        <SheetFooter className="justify-end">
          <Button variant="secondary" onClick={close}>
            {t.common.cancel}
          </Button>
          <Button disabled={!editor.canSave} onClick={() => void editor.save()}>
            {t.skills.submitCreate}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

export function SkillDetailSheet() {
  const { skillId = '' } = useParams()
  const navigate = useNavigate()
  const skill = useSkill(skillId)
  const data = skill.data
  const notFound = isNotFound(skill.error)

  let body: ReactNode
  if (notFound) {
    body = <p className="text-[14px] text-ink-500">{t.skills.notFound}</p>
  } else if (!data) {
    // Once data exists, a failed background refresh is only reported in the drawer; the editor and confirmation dialogs are
    // not unmounted, so unsaved edits are not lost.
    body = skill.isError ? <ErrorAlert error={skill.error} /> : <p className="text-[14px] text-ink-500">{t.common.loading}</p>
  }

  return (
    <Sheet open onOpenChange={(open) => !open && void navigate('/skills')}>
      {/* Without template data the drawer has no description text, so drop aria-describedby. */}
      <SheetContent {...(data && !notFound ? {} : { 'aria-describedby': undefined })}>
        <SheetHeader>
          {data && !notFound ? (
            <>
              <SheetTitle className="font-mono">{data.name}</SheetTitle>
              <SheetDescription>{data.description}</SheetDescription>
              <span className="text-[12.5px] font-bold text-white">{t.skills.updated(data.updatedAt)}</span>
            </>
          ) : (
            <SheetTitle className="font-display-cn text-[28px] font-normal">{t.skills.detailTitle}</SheetTitle>
          )}
        </SheetHeader>
        {data && !notFound ? <SkillDetailBody skillId={skillId} data={data} refreshError={skill.isError ? skill.error : null} /> : <SheetBody>{body}</SheetBody>}
      </SheetContent>
    </Sheet>
  )
}

// Body and footer of the detail drawer: the file list, the SKILL.md editor and replace; the footer holds delete and save.
function SkillDetailBody({ skillId, data, refreshError }: { skillId: string; data: Schemas['SkillTemplate']; refreshError: unknown }) {
  const navigate = useNavigate()
  const update = useUpdateSkillMarkdown(skillId)
  const replace = useReplaceSkill(skillId)
  const remove = useDeleteSkill(skillId)
  const [replacement, setReplacement] = useState<Blob | null>(null)
  const [deleting, setDeleting] = useState(false)
  const editorId = useId()
  // When the server content changes (updatedAt changes), reset the editor to the new content; a failed background refresh
  // keeps the edits.
  const editor = useSkillMdEditor(data.skillMd, (content) => update.mutateAsync(content), data.updatedAt)

  return (
    <>
      <SheetBody>
        <ErrorAlert error={refreshError} />
        <section className="flex flex-col gap-2.5">
          <h3 className={sectionTitleClass}>
            {t.skills.fileList} <span className="font-display-latin text-[16px] font-extrabold text-ink-500">{t.skills.files(data.files.length)}</span>
          </h3>
          <ul className="flex flex-col gap-1.5" aria-label={t.skills.fileList}>
            {data.files.map((file) => (
              <li key={file.path} className="grid grid-cols-[minmax(0,1fr)_max-content] overflow-hidden rounded-ticket border border-hairline bg-surface-control [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white">
                <span className="px-3.5 py-[9px] font-mono text-[13px] [overflow-wrap:anywhere]">{file.path}</span>
                <span className="flex items-center border-l-2 border-dashed border-ink/20 px-3.5 text-[12.5px] font-bold text-ink-500">{t.common.bytes(file.size)}</span>
              </li>
            ))}
          </ul>
        </section>
        <section className="flex flex-col gap-2.5">
          <label htmlFor={editorId} className={sectionTitleClass}>
            {t.skills.skillMd}
          </label>
          <Textarea id={editorId} className={cn(editorClass, 'min-h-[380px]')} value={editor.content} onChange={(e) => editor.setContent(e.target.value)} />
          <ErrorAlert error={editor.error} />
        </section>
        <section className="flex flex-col gap-2.5 rounded-card bg-tint-blue px-[18px] py-4">
          <h3 className={sectionTitleClass}>{t.skills.replace}</h3>
          <SkillUpload buttonClassName="[[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:hover:bg-white/80" onArchive={async (archive) => setReplacement(archive)} />
        </section>
      </SheetBody>
      <SheetFooter>
        <Button size="sm" variant="destructive-link" onClick={() => setDeleting(true)}>
          {t.common.delete}
        </Button>
        <Button className="ml-auto" disabled={!editor.canSave} onClick={() => void editor.save()}>
          {t.common.save}
        </Button>
      </SheetFooter>
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
  )
}
