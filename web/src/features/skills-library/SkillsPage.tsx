import { useId, useState, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import { Link, Outlet, useNavigate, useParams } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
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
import { pageTitleClass } from '@/lib/page-title'

// 模板卡片的倾斜：只转 ::before 背景层，卡片本体（链接）保持水平。每屏倾斜元素不超过 6 个，
// 网格最多三列、一屏约四行，所以每四张卡片只倾斜一张（落在对角线上），角度在 ±0.6°～1.2° 之间交替。
const TILTS = ['[--tilt:-1deg]', '[--tilt:0.8deg]', '[--tilt:-0.6deg]', '[--tilt:1.1deg]']
const tiltClass = (index: number) => (index % 4 === 0 ? TILTS[(index / 4) % TILTS.length] : '[--tilt:0deg]')

// 抽屉的开合由地址决定：/skills/:skillId 打开详情抽屉，/skills/new 打开在线新建抽屉，关闭后回到 /skills。
// 抽屉作为子路由渲染在网格之上，所以刷新或直接打开这两个地址时网格照常显示。
export function SkillsPage() {
  const skills = useSkills()
  const navigate = useNavigate()
  const create = useCreateSkillFromArchive()
  const { skillId } = useParams()
  const list = skills.data?.skills ?? []
  const empty = skills.isSuccess && list.length === 0
  const newLink = (
    <Button size="lg" asChild>
      <Link to="/skills/new">
        <PlusIcon aria-hidden="true" />
        {t.skills.create}
      </Link>
    </Button>
  )
  // 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。
  return (
    <div className="relative h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[1240px] flex-col gap-6 px-4 pt-[34px] pb-16 sm:px-[30px]">
        <SkillUpload size="lg" onArchive={async (archive) => navigate(`/skills/${(await create.mutateAsync(archive)).id}`)}>
          {({ actions, feedback }) => (
            <>
              <div className="flex flex-wrap items-end gap-x-5 gap-y-3.5">
                <h1 className={pageTitleClass}>{t.skills.title}</h1>
                {skills.isSuccess && (
                  <span className="pb-2 font-display-latin text-[34px] leading-none font-extrabold text-pink-text [text-shadow:3px_2px_0_var(--riso-blue)]">
                    <span aria-hidden="true">{list.length}</span>
                    <span className="sr-only">{t.skills.count(list.length)}</span>
                  </span>
                )}
                {/* 空库时上传与新建入口只放在空状态里，不重复出现在工具栏。 */}
                {!empty && (
                  <div className="ml-auto flex flex-wrap gap-2.5 pb-1.5">
                    {actions}
                    {newLink}
                  </div>
                )}
              </div>
              {feedback}
              {empty && (
                <EmptyLibrary>
                  {actions}
                  {newLink}
                </EmptyLibrary>
              )}
            </>
          )}
        </SkillUpload>
        {skills.isError && <ErrorAlert error={skills.error} />}
        {skills.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
        {list.length > 0 && (
          <ul className="grid grid-cols-[repeat(auto-fill,minmax(min(320px,100%),1fr))] gap-[22px]" aria-label={t.skills.title}>
            {list.map((skill, index) => (
              <li key={skill.id} className="flex">
                <TemplateCard skill={skill} selected={skill.id === skillId} className={tiltClass(index)} />
              </li>
            ))}
          </ul>
        )}
      </div>
      <Outlet />
    </div>
  )
}

// 模板卡片：名称（等宽）、文件数、描述与更新时间。整张卡片是打开详情抽屉的链接；当前打开的卡片为黄底粉投影。
function TemplateCard({ skill, selected, className }: { skill: Schemas['SkillTemplateSummary']; selected: boolean; className: string }) {
  return (
    <Link
      to={`/skills/${skill.id}`}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        'riso-tilt flex w-full flex-col gap-3 rounded-panel p-5 text-ink no-underline [--tilt-bg:#fff] hover:[--tilt-bg:var(--tint-yellow)] aria-[current=true]:[--tilt-bg:var(--riso-yellow)] aria-[current=true]:[--tilt-shadow:5px_5px_0_var(--riso-pink)]',
        className,
      )}
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

function EmptyLibrary({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-[18px] rounded-dialog bg-paper-raised px-6 py-16 text-center shadow-print-rest">
      {/* 空书架：两本叠印的书，倾斜画在 ::before 上。 */}
      <span aria-hidden="true" className="flex h-[100px] w-[140px] items-start pl-2.5">
        <span className="riso-tilt mt-3.5 h-20 w-[60px] shrink-0 rounded-lg [--tilt-bg:var(--riso-blue)] [--tilt-shadow:none] [--tilt:-8deg]" />
        <span className="riso-tilt mt-1.5 -ml-5 h-[84px] w-[60px] shrink-0 rounded-lg mix-blend-multiply [--tilt-bg:var(--riso-pink)] [--tilt-shadow:none] [--tilt:6deg]" />
      </span>
      <p className="font-display-cn text-[34px] leading-[1.2]">{t.skills.empty}</p>
      <div className="flex flex-wrap justify-center gap-2.5">{children}</div>
    </div>
  )
}

// 抽屉底栏：在正文滚动区域之外，始终可见。
function SheetFooterBar({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn('flex flex-wrap items-center gap-x-4 gap-y-2.5 px-[26px] py-4 shadow-[inset_0_3px_0_rgb(24_38_94/.1)]', className)}>{children}</div>
}

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
        <SheetFooterBar className="justify-end">
          <Button variant="secondary" onClick={close}>
            {t.common.cancel}
          </Button>
          <Button disabled={!editor.canSave} onClick={() => void editor.save()}>
            {t.skills.submitCreate}
          </Button>
        </SheetFooterBar>
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
    // 已有数据时，后台刷新失败只在抽屉中提示，不卸载编辑器与确认框，避免丢失未保存的编辑。
    body = skill.isError ? <ErrorAlert error={skill.error} /> : <p className="text-[14px] text-ink-500">{t.common.loading}</p>
  }

  return (
    <Sheet open onOpenChange={(open) => !open && void navigate('/skills')}>
      {/* 没有模板数据时抽屉没有说明文字，去掉 aria-describedby。 */}
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

// 详情抽屉的正文与底栏：文件列表、SKILL.md 编辑器、替换；底栏为删除与保存。
function SkillDetailBody({ skillId, data, refreshError }: { skillId: string; data: Schemas['SkillTemplate']; refreshError: unknown }) {
  const navigate = useNavigate()
  const update = useUpdateSkillMarkdown(skillId)
  const replace = useReplaceSkill(skillId)
  const remove = useDeleteSkill(skillId)
  const [replacement, setReplacement] = useState<Blob | null>(null)
  const [deleting, setDeleting] = useState(false)
  const editorId = useId()
  // 服务端内容更新（updatedAt 变化）后用新内容重置编辑器；后台刷新失败时保留编辑。
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
              <li key={file.path} className="grid grid-cols-[minmax(0,1fr)_max-content] overflow-hidden rounded-ticket bg-white">
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
          <SkillUpload buttonClassName="bg-white hover:bg-white/80" onArchive={async (archive) => setReplacement(archive)} />
        </section>
      </SheetBody>
      <SheetFooterBar>
        <Button size="sm" variant="destructive-link" onClick={() => setDeleting(true)}>
          {t.common.delete}
        </Button>
        <Button className="ml-auto" disabled={!editor.canSave} onClick={() => void editor.save()}>
          {t.common.save}
        </Button>
      </SheetFooterBar>
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
