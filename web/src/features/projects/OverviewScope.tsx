import { useId } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import { agentsPath, PLATFORM } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useScopeLabel, type OverviewFilter } from './overview-filter'
import { ScopeIcon } from './ScopeIcon'

// 概览标题行右侧的范围筛选：“范围”标签加原生下拉，选项为全部、各项目（分组）、平台（交接说明第 11.6 节）。
// 地址中的项目找不到时，多一个不可选的“{名称}（找不到）”作为当前值。
export function OverviewScopeSelect({
  filter,
  projects,
  onSelect,
}: {
  filter: OverviewFilter
  projects: Schemas['Project'][] | undefined
  onSelect: (value: string) => void
}) {
  const id = useId()
  const value = filter.kind === 'all' ? '' : filter.kind === 'platform' ? PLATFORM : `project:${filter.name}`
  const missing = filter.kind === 'project' && !filter.project
  return (
    <span className="flex w-full items-center gap-2.5 sm:w-auto">
      <label htmlFor={id} className="shrink-0 text-[13.5px] font-black">
        {t.projects.overview.range}
      </label>
      <select id={id} value={value} onChange={(event) => onSelect(event.target.value)} className={cn(selectClass, 'w-full sm:w-auto sm:min-w-[220px]')}>
        <option value="">{t.projects.overview.all}</option>
        {missing && (
          <option value={value} disabled>
            {filter.project === null ? t.projects.overview.missingOption(filter.name) : filter.name}
          </option>
        )}
        <optgroup label={t.projects.overview.projectGroup}>
          {(projects ?? []).map((project) => (
            <option key={project.id} value={`project:${project.name}`}>
              {project.name}
            </option>
          ))}
        </optgroup>
        <option value={PLATFORM}>{t.projects.overview.platform}</option>
      </select>
    </span>
  )
}

// 所属标签：白底、细描边、6 圆角；项目带文件夹图标，平台带平台图标；已删除的项目用虚线描边。
// 只在概览选“全部”时显示（由 ScopeLabelContext 决定），选定范围后不显示。
export function ScopeTag({ scope, projectId }: { scope: Schemas['AgentScope']; projectId: string | null }) {
  const labelOf = useScopeLabel()
  if (!labelOf) return null
  const label = labelOf(scope, projectId)
  return (
    <span
      className={cn(
        'inline-flex max-w-[220px] shrink-0 items-center gap-1 rounded-[6px] bg-white px-1.5 text-[12.5px] leading-[22px] font-bold',
        label.kind === 'deleted' ? 'border-[1.5px] border-dashed border-ink/30 text-ink-500' : 'text-ink-700 shadow-[inset_0_0_0_1.5px_rgb(24_38_94/.3)]',
      )}
    >
      {label.kind !== 'deleted' && <ScopeIcon kind={label.kind} className="size-3 shrink-0" />}
      <span className="truncate">
        {label.kind === 'platform' ? t.projects.overview.platform : label.kind === 'project' ? label.name : t.projects.overview.deletedProject}
      </span>
    </span>
  )
}

const panel = 'flex flex-col items-center gap-[18px] rounded-dialog bg-paper-raised px-6 py-14 text-center shadow-print-rest'

// 地址中的项目找不到：三块内容换成说明和“回到全部”。
export function OverviewScopeNotFound({ name, onBack }: { name: string; onBack: () => void }) {
  const titleId = useId()
  return (
    <section role="alert" aria-labelledby={titleId} className={panel}>
      <span aria-hidden="true" className="relative h-[130px] w-[200px] shrink-0">
        <span className="riso-halftone absolute top-[5px] left-2.5 size-[120px] rounded-full bg-riso-blue" />
        <span className="absolute top-[5px] left-[76px] size-[116px] rounded-full border-[3px] border-dashed border-pink-text" />
      </span>
      <h2 id={titleId} className="font-display-cn text-[40px] leading-[1.2] font-normal text-riso-blue">
        {t.projects.overview.notFoundTitle(name)}
      </h2>
      <p className="text-[14.5px] text-ink-700">{t.projects.overview.notFoundBody}</p>
      <Button size="lg" className="h-12 px-[26px] text-base shadow-print-pink" onClick={onBack}>
        {t.projects.overview.backToAll}
      </Button>
    </section>
  )
}

// 选定的项目或平台中还没有 Agent：说明这里只显示该范围，并提供进入它的 Agent 页的入口。
export function OverviewScopeEmpty({ project }: { project: Schemas['Project'] | null }) {
  const titleId = useId()
  const text = project
    ? { title: t.projects.overview.emptyProject.title(project.name), body: t.projects.overview.emptyProject.body, action: t.projects.overview.emptyProject.action(project.name) }
    : t.projects.overview.emptyPlatform
  return (
    <section aria-labelledby={titleId} className={panel}>
      <h2 id={titleId} className="font-display-cn text-[40px] leading-[1.2] font-normal text-riso-blue">
        {text.title}
      </h2>
      <p className="text-[14.5px] text-ink-700">{text.body}</p>
      <Button asChild size="lg" className="h-12 px-[26px] text-base shadow-print-pink">
        <Link to={agentsPath(project ? { kind: 'project', project } : { kind: 'platform' })}>{text.action}</Link>
      </Button>
    </section>
  )
}
