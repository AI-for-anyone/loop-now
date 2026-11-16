import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { agentsUrl, apiUrl, context, makeAgent, makeMessagePage, makeStatus, toListItem } from './api'
import { emptySelections } from './project-credentials'

// Default endpoint handlers for the whole console. MSW matches in order within one server.use, so overrides in tests must come before them.
export function baseHandlers(agents: Schemas['Agent'][] = [makeAgent()]) {
  return [
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true } })),
    http.get(agentsUrl, () => HttpResponse.json({ agents: agents.map((a) => toListItem(a)) })),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}`), () => HttpResponse.json(a))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/status`), () => HttpResponse.json(makeStatus(a.id)))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/model-auth`), () => HttpResponse.json({ credential: a.modelCredential, operation: null }))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/credential-selections`), () => HttpResponse.json(emptySelections(a)))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/skills`), () => HttpResponse.json({ skills: [] }))),
    ...agents.map((a) => http.get(apiUrl(`/agents/${a.id}/messages`), () => HttpResponse.json(makeMessagePage([])))),
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [] })),
    http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) => HttpResponse.json({ providerId: String(params.providerId), models: [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })),
  ]
}

// The left sidebar and its links; the one with aria-current page is the current item. Link
// text includes the status labels and the screen-reader text “待你回复” (Needs your reply).
export const sidebar = () => screen.getByRole('navigation', { name: t.shell.nav })
export const sidebarLinks = () => within(sidebar()).getAllByRole('link')
export const currentSidebarLinks = () => sidebarLinks().filter((link) => link.getAttribute('aria-current') === 'page')
export const scopeSwitcher = (name: string) => within(sidebar()).getByRole('button', { name: t.shell.scope.label(name) })
// Navigating makes the sidebar re-render for the new scope; for a query right afterwards, use this async variant to wait a tick.
export const findScopeSwitcher = (name: string) => within(sidebar()).findByRole('button', { name: `切换范围，当前：${name}` })
