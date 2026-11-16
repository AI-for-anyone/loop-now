import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useProjectPlugin, useSaveProjectPlugin } from '@/api/plugins'
import { useProjects } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { describeValue, fieldsOf } from './schema-form'
import { Quiet } from './parts'

const text = t.plugins.detail
const projectPluginsPath = (project: Schemas['Project']) => `/p/${encodeURIComponent(project.name)}/plugins`

// One row per project: project name, how many fields are overridden, and the enable switch on the right. When the switch saves, it sends
// along the project's existing project-level config and revision, so the config itself doesn't change.
function EnableRow({ project, pluginId, schema }: { project: Schemas['Project']; pluginId: string; schema: unknown }) {
  const { item, error } = useProjectPlugin(project.id, pluginId)
  const save = useSaveProjectPlugin(project.id, pluginId)
  const overrides = item ? Object.keys(item.config.values as Record<string, unknown>).length + item.config.secretsSet.length : 0
  const fields = fieldsOf(schema, 'project')
  const first = item && fields.find((field) => (item.config.values as Record<string, unknown>)[field.name] !== undefined)
  return (
    <li className="grid min-w-0 gap-1 border-b-[1.5px] border-dashed border-ink/25 px-1 py-[11px]">
      <div className="flex min-w-0 items-center gap-3">
        <Link to={projectPluginsPath(project)} className="min-w-0 truncate text-[14px] font-black outline-none hover:underline focus-visible:shadow-focus">
          {project.name}
        </Link>
        {overrides > 0 && <span className="shrink-0 text-[12.5px] text-ink-700">{text.overrides(overrides)}</span>}
        <Switch
          className="ml-auto"
          aria-label={t.plugins.project.enableIn(project.name, item ? localizedText(item.displayName) : pluginId)}
          checked={item?.enabled ?? false}
          disabled={!item || save.isPending}
          onCheckedChange={(enabled) => item && save.mutate({ enabled, values: item.config.values, expectedRevision: item.config.revision })}
        />
      </div>
      {first && item && (
        <p className="truncate text-[12.5px] text-ink-700">
          {first.label} → {describeValue(first, (item.config.values as Record<string, unknown>)[first.name])}
        </p>
      )}
      <ErrorAlert error={error ?? save.error} />
    </li>
  )
}

// Which projects enable it: one switch per project.
export function ProjectEnableList({ pluginId, schema }: { pluginId: string; schema: unknown }) {
  const projects = useProjects()
  const list = projects.data?.projects ?? []
  if (projects.error) return <ErrorAlert error={projects.error} />
  if (projects.isPending) return <Quiet>{t.common.loading}</Quiet>
  if (!list.length) return <Quiet>{text.noProjects}</Quiet>
  return (
    // Redesign step 5: a solid line at the header, dashed lines between projects (general rule 9).
    <ul className="grid min-w-0 border-t-2 border-ink">
      {list.map((project) => (
        <EnableRow key={project.id} project={project} pluginId={pluginId} schema={schema} />
      ))}
    </ul>
  )
}
