import { createContext, useContext } from 'react'
import { useSearchParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { useProjects } from '@/api/queries'
import { PLATFORM, scopeQueryKey, type ScopeQuery } from '@/lib/scope'

// 概览页的范围筛选，写在概览地址的查询参数中：?project=<项目名> 或 ?platform=1，都没有时为全部。
// 顶栏从同一地址读取当前选择，全部范围对应未选择项目。
export type OverviewFilter =
  | { kind: 'all' }
  | { kind: 'platform' }
  // project 为 undefined：项目列表还没加载完；null：找不到同名的未删除项目。
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

  // 选择项：'' 为全部，PLATFORM 为平台，其余为项目名。
  const select = (value: string) =>
    setParams(value === '' ? {} : value === PLATFORM ? { platform: '1' } : { project: value.slice('project:'.length) })

  // 传给概览事件流、用量与异常的范围参数；项目还没解析出来时为 null，这时不请求。
  const query: ScopeQuery | null =
    filter.kind === 'all' ? {} : filter.kind === 'platform' ? { platform: true } : filter.project ? { projectId: filter.project.id } : null
  return { filter, projects: list, select, query, key: query ? scopeQueryKey(query) : null }
}

// 选“全部”时，运行状态、用量与异常的各行标出所属项目或“平台”；选定范围后不标。
export type ScopeLabel = { kind: 'platform' } | { kind: 'project'; name: string } | { kind: 'deleted' }
export const ScopeLabelContext = createContext<((scope: Schemas['AgentScope'], projectId: string | null) => ScopeLabel) | null>(null)

export const useScopeLabel = () => useContext(ScopeLabelContext)
