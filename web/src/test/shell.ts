import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { agentsUrl, apiUrl, context, makeAgent, makeStatus, toListItem } from './api'
import { emptySelections } from './project-credentials'

// 整个控制台的默认接口处理器。MSW 在一次 server.use 中按顺序匹配，测试中的覆盖要写在它们前面。
export function baseHandlers(agents: Schemas['Agent'][] = [makeAgent()]) {
  return [
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, postgres: { ok: true }, redis: { ok: true } })),
    http.get(agentsUrl, () => HttpResponse.json({ agents: agents.map((a) => toListItem(a)) })),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}`), () => HttpResponse.json(a))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/status`), () => HttpResponse.json(makeStatus(a.id)))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/model-auth`), () => HttpResponse.json({ credential: a.modelCredential, operation: null }))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/credential-selections`), () => HttpResponse.json(emptySelections(a)))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/skills`), () => HttpResponse.json({ skills: [] }))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/messages`), () => HttpResponse.json({ messages: [], hasMore: false }))),
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [] })),
    http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) => HttpResponse.json({ providerId: String(params.providerId), models: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
  ]
}

// 左侧栏与其中的链接；aria-current 为 page 的是当前项。链接文字含状态标签与“待你回复”的读屏文字。
export const sidebar = () => screen.getByRole('navigation', { name: t.shell.nav })
export const sidebarLinks = () => within(sidebar()).getAllByRole('link')
export const currentSidebarLinks = () => sidebarLinks().filter((link) => link.getAttribute('aria-current') === 'page')
export const scopeSwitcher = (name: string) => within(sidebar()).getByRole('button', { name: t.shell.scope.label(name) })
// 导航会触发侧栏按新范围重渲染，随后立即查询时用这个异步变体等一拍。
export const findScopeSwitcher = (name: string) => within(sidebar()).findByRole('button', { name: `切换范围，当前：${name}` })
