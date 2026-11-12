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
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet as PaperSheet } from '@/components/Sheet'

// 上传目录、上传 zip：白底、藏青描边加藏青投影的胶囊；在线新建是本页唯一的荧光粉主按钮（改版通用规则第 7 条）。
const uploadButtonClass = 'border-2 border-ink bg-white shadow-[3px_3px_0_var(--ink)] hover:bg-paper-raised'

// 抽屉的开合由地址决定：/skills/:skillId 打开详情抽屉，/skills/new 打开在线新建抽屉，关闭后回到 /skills。
// 抽屉作为子路由渲染在网格之上，所以刷新或直接打开这两个地址时网格照常显示。
// 改版：页头是共用的页面标题（旁边写着共几个模板，有模板时还有上传与新建入口）；模板与空状态排在一张纸上。
export function SkillsPage() {
  const skills = useSkills()
  const navigate = useNavigate()
  const create = useCreateSkillFromArchive()
  const { skillId } = useParams()
  const list = skills.data?.skills ?? []
  const empty = skills.isSuccess && list.length === 0
  const newLink = (
    <Button size={empty ? 'lg' : 'xs'} variant="interrupt" className="border-2 border-ink" asChild>
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
            {/* 空库时上传与新建入口只放在空状态里，不重复出现在页头。 */}
            {!empty && skills.isSuccess && (
              <>
                {actions}
                {newLink}
              </>
            )}
          </PageHeader>
          <PaperSheet className="mt-2 flex-1">
            {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
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

// 模板卡片：名称（等宽）、文件数、描述与更新时间。整张卡片是打开详情抽屉的链接。
// 改版：白底、2px 藏青描边、藏青实色投影的卡片；悬停浅黄，当前打开的一张黄底加荧光粉投影（选中态一律黄色）。
function TemplateCard({ skill, selected }: { skill: Schemas['SkillTemplateSummary']; selected: boolean }) {
  return (
    <Link
      to={`/skills/${skill.id}`}
      aria-current={selected ? 'true' : undefined}
      className="mr-1 mb-1 flex w-full flex-col gap-3 rounded-card border-2 border-ink bg-white p-5 text-ink no-underline shadow-[4px_4px_0_var(--ink)] outline-none hover:bg-tint-yellow focus-visible:shadow-focus aria-[current=true]:bg-riso-yellow aria-[current=true]:shadow-print-pink"
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

// 空状态（改版）：一片网点、两张叠印的卡片（孔版蓝、荧光粉）和一个黄色的圆，下面是黄色记号笔标题与三个入口。
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
