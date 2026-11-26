import { useId } from 'react'
import { Link } from 'react-router'
import { SelectChevron } from '@/components/SelectChevron'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { agentsPath, PLATFORM } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { useScopeLabel, type OverviewFilter } from './overview-filter'
import { ScopeIcon } from './ScopeIcon'

// The scope filter in the overview header's meta info: a dashed box reading “范围” (Scope) followed by the current scope, with a
// small arrow on the right. It is still a native select (keyboard and screen readers work as before); the options are All, each
// project (in a group) and Platform (handoff notes, section 11.6). When the project in the URL can't be found, an extra disabled
// option “{名称}（找不到）” ({name} (not found)) serves as the current value.
export function OverviewScopeSelect({
  filter,
  projects,
  onSelect,
  allLabel = t.projects.overview.all,
  className,
}: {
  filter: OverviewFilter
  projects: Schemas['Project'][] | undefined
  onSelect: (value: string) => void
  allLabel?: string
  className?: string
}) {
  const id = useId()
  const value = filter.kind === 'all' ? '' : filter.kind === 'platform' ? PLATFORM : `project:${filter.name}`
  const missing = filter.kind === 'project' && !filter.project
  return (
    <span
      className={cn(
        'relative inline-flex h-8 max-w-full min-w-0 items-center gap-2 rounded-ticket border border-hairline-strong bg-surface-control pr-1.5 pl-2.5 text-[12.5px] font-medium text-ink focus-within:shadow-focus [[data-skin=plate]_&]:rounded-[4px] [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/45 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:font-bold',
        className,
      )}
    >
      <label htmlFor={id} className="shrink-0 text-ink-500">
        {t.projects.overview.range}
      </label>
      <select
        id={id}
        value={value}
        onChange={(event) => onSelect(event.target.value)}
        className="max-w-[200px] min-w-0 cursor-pointer appearance-none truncate bg-transparent pr-[18px] font-medium text-ink outline-none [[data-skin=plate]_&]:font-bold"
      >
        <option value="">{allLabel}</option>
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
      <SelectChevron />
    </span>
  )
}

// The ownership tag: white background, thin outline, radius 6; projects get a folder icon, the platform gets the platform icon;
// deleted projects get a dashed outline.
// Only shown when the overview is set to “全部” (All) (decided by ScopeLabelContext); not shown once a scope is selected.
export function ScopeTag({ scope, projectId }: { scope: Schemas['AgentScope']; projectId: string | null }) {
  const labelOf = useScopeLabel()
  if (!labelOf) return null
  const label = labelOf(scope, projectId)
  return (
    <span
      className={cn(
        'inline-flex max-w-[220px] shrink-0 items-center gap-1 rounded-[6px] bg-surface-control px-1.5 text-[12.5px] leading-[22px] font-medium [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:font-bold',
        label.kind === 'deleted'
          ? 'border border-hairline text-ink-500 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/30'
          : 'border border-hairline-strong text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:shadow-[inset_0_0_0_1.5px_rgb(24_38_94/.3)]',
      )}
    >
      {label.kind !== 'deleted' && <ScopeIcon kind={label.kind} className="size-3 shrink-0" />}
      <span className="truncate">
        {label.kind === 'platform' ? t.projects.overview.platform : label.kind === 'project' ? label.name : t.projects.overview.deletedProject}
      </span>
    </span>
  )
}

// The not-found and empty-scope notices sit directly on the overview sheet, without wrapping them in another card.
const panel = 'flex flex-col items-center gap-[18px] px-6 py-14 text-center'

// The project in the URL can't be found: the three sections are replaced by a notice and “回到全部” (Back to all).
export function OverviewScopeNotFound({ name, onBack }: { name: string; onBack: () => void }) {
  const titleId = useId()
  return (
    <section role="alert" aria-labelledby={titleId} className={panel}>
      <span aria-hidden="true" className="relative hidden h-[130px] w-[200px] shrink-0 [[data-skin=plate]_&]:inline-block">
        <span className="riso-halftone absolute top-[5px] left-2.5 size-[120px] rounded-full bg-riso-blue" />
        <span className="absolute top-[5px] left-[76px] size-[116px] rounded-full border-[3px] border-dashed border-pink-text" />
      </span>
      <h2 id={titleId} className="font-display-cn text-[28px] leading-[1.2] font-semibold text-ink [[data-skin=plate]_&]:text-[40px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:text-riso-blue">
        {t.projects.overview.notFoundTitle(name)}
      </h2>
      <p className="text-[14.5px] text-ink-700">{t.projects.overview.notFoundBody}</p>
      <Button size="lg" className="h-11 px-6 text-[15px] [[data-skin=plate]_&]:h-12 [[data-skin=plate]_&]:px-[26px] [[data-skin=plate]_&]:text-base [[data-skin=plate]_&]:shadow-print-pink" onClick={onBack}>
        {t.projects.overview.backToAll}
      </Button>
    </section>
  )
}

// The selected project or the platform has no Agents yet: explain that only this scope is shown here, and offer a link to its
// Agents page.
export function OverviewScopeEmpty({ project }: { project: Schemas['Project'] | null }) {
  const titleId = useId()
  const text = project
    ? { title: t.projects.overview.emptyProject.title(project.name), body: t.projects.overview.emptyProject.body, action: t.projects.overview.emptyProject.action(project.name) }
    : t.projects.overview.emptyPlatform
  return (
    <section aria-labelledby={titleId} className={panel}>
      <h2 id={titleId} className="font-display-cn text-[28px] leading-[1.2] font-semibold text-ink [[data-skin=plate]_&]:text-[40px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:text-riso-blue">
        {text.title}
      </h2>
      <p className="text-[14.5px] text-ink-700">{text.body}</p>
      <Button asChild size="lg" className="h-11 px-6 text-[15px] [[data-skin=plate]_&]:h-12 [[data-skin=plate]_&]:px-[26px] [[data-skin=plate]_&]:text-base [[data-skin=plate]_&]:shadow-print-pink">
        <Link to={agentsPath(project ? { kind: 'project', project } : { kind: 'platform' })}>{text.action}</Link>
      </Button>
    </section>
  )
}
