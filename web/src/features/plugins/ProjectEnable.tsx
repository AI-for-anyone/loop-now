import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useProjectPlugin, useSaveProjectPlugin } from '@/api/plugins'
import { useProjects } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { Quiet } from './parts'
import { pluginPath } from './paths'
import { describeValue, fieldsOf } from './schema-form'

const text = t.plugins.detail
const projectPluginsPath = (project: Schemas['Project']) => `/p/${encodeURIComponent(project.name)}/plugins`

// One compact row per project: project name, override count, inline "Configure ›" link when enabled, and enable switch on the right.
// When the switch saves, it sends along the project's existing project-level config and revision, so the config itself doesn't change.
function EnableRow({ project, pluginId, schema }: { project: Schemas['Project']; pluginId: string; schema: unknown }) {
  const { item, error } = useProjectPlugin(project.id, pluginId)
  const save = useSaveProjectPlugin(project.id, pluginId)
  const overrides = item ? Object.keys(item.config.values as Record<string, unknown>).length + item.config.secretsSet.length : 0
  const fields = fieldsOf(schema, 'project')
  const first = item && fields.find((field) => (item.config.values as Record<string, unknown>)[field.name] !== undefined)
  return (
    <li className="grid min-w-0 gap-1 border-b border-hairline px-1 py-2 last:border-b-0 [[data-skin=plate]_&]:border-b-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/25">
      <div className="flex min-w-0 items-center gap-2.5">
        <Link to={projectPluginsPath(project)} className="min-w-0 truncate text-[13.5px] font-semibold outline-none hover:underline focus-visible:shadow-focus [[data-skin=plate]_&]:font-black">
          {project.name}
        </Link>
        {overrides > 0 && <span className="shrink-0 text-[12px] text-ink-500">{text.overrides(overrides)}</span>}
        <div className="ml-auto flex shrink-0 items-center gap-3">
          {item?.enabled && (
            <Link
              to={pluginPath(pluginId, 'config')}
              className="text-[12px] font-medium text-ink-700 underline-offset-3 outline-none hover:text-ink hover:underline focus-visible:shadow-focus [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-riso-blue"
            >
              {text.configure} ›
            </Link>
          )}
          <Switch
            aria-label={t.plugins.project.enableIn(project.name, item ? localizedText(item.displayName) : pluginId)}
            checked={item?.enabled ?? false}
            disabled={!item || save.isPending}
            onCheckedChange={(enabled) => item && save.mutate({ enabled, values: item.config.values, expectedRevision: item.config.revision })}
          />
        </div>
      </div>
      {first && item && (
        <p className="truncate text-[12px] text-ink-500">
          {first.label} → {describeValue(first, (item.config.values as Record<string, unknown>)[first.name])}
        </p>
      )}
      <ErrorAlert error={error ?? save.error} />
    </li>
  )
}

// Which projects enable it: one compact switch row per project.
export function ProjectEnableList({ pluginId, schema }: { pluginId: string; schema: unknown }) {
  const projects = useProjects()
  const list = projects.data?.projects ?? []
  if (projects.error) return <ErrorAlert error={projects.error} />
  if (projects.isPending) return <Quiet>{t.common.loading}</Quiet>
  if (!list.length) return <Quiet>{text.noProjects}</Quiet>
  return (
    <ul className="grid min-w-0 border-t border-hairline [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-ink">
      {list.map((project) => (
        <EnableRow key={project.id} project={project} pluginId={pluginId} schema={schema} />
      ))}
    </ul>
  )
}
