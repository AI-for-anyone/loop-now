import { createContext, useContext } from 'react'
import { useSearchParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { useProjects } from '@/api/queries'
import { PLATFORM, scopeQueryKey, type ScopeQuery } from '@/lib/scope'

// The overview page's scope filter, stored in the overview URL's query parameters: ?project=<project name> or ?platform=1; with
// neither, it is All.
// The top bar reads the current selection from the same URL; the All scope corresponds to no project being selected.
export type OverviewFilter =
  | { kind: 'all' }
  | { kind: 'platform' }
  // project is undefined: the project list hasn't finished loading; null: no undeleted project with this name exists.
  | { kind: 'project'; name: string; project: Schemas['Project'] | null | undefined }

export function useOverviewFilter() {
  const [params, setParams] = useSearchParams()
  const projects = useProjects()
  const list = projects.data?.projects
  const name = params.get('project')
  const filter: OverviewFilter =
    name !== null
      ? { kind: 'project', name, project: list ? (list.find((p) => p.name === name) ?? null) : undefined }
      : params.get('platform') === '1'
        ? { kind: 'platform' }
        : { kind: 'all' }

  // The selected value: '' is All, PLATFORM is the platform, anything else is a project name.
  const select = (value: string) =>
    setParams(value === '' ? {} : value === PLATFORM ? { platform: '1' } : { project: value.slice('project:'.length) })

  // The scope parameters passed to the overview event stream, usage and anomalies; null while the project hasn't been resolved
  // yet, in which case nothing is requested.
  const query: ScopeQuery | null =
    filter.kind === 'all' ? {} : filter.kind === 'platform' ? { platform: true } : filter.project ? { projectId: filter.project.id } : null
  return { filter, projects: list, select, query, key: query ? scopeQueryKey(query) : null }
}

// With “全部” (All) selected, each row in run status, usage and anomalies is tagged with its project or “平台” (Platform);
// once a scope is selected, rows are not tagged.
export type ScopeLabel = { kind: 'platform' } | { kind: 'project'; name: string } | { kind: 'deleted' }
export const ScopeLabelContext = createContext<((scope: Schemas['AgentScope'], projectId: string | null) => ScopeLabel) | null>(null)

export const useScopeLabel = () => useContext(ScopeLabelContext)
