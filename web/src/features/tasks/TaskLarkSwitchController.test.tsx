import { useState } from 'react'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { type Schemas } from '@/api/client'
import { apiUrl, defaultProject } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { taskId } from '@/test/tasks'
import { larkUserAccount } from '@/test/lark-login'
import { larkSwitchInput, larkSwitchReceipt, larkSwitchDetail as detail } from '@/test/lark-switch'
import { taskLarkSwitchLocatorKey } from '@/lib/useOriginalActionLocator'
import { t } from '@/i18n'
import { TaskLarkSwitchController } from './TaskLarkSwitchController'

const copy = t.credentials.lark

const endpoint = apiUrl(`/tasks/${taskId}/lark-account-switches`), account = larkUserAccount()
function setup() {
  server.use(http.get(apiUrl(`/projects/${defaultProject.id}/credential-accounts`), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts: [account] })))
}
function renderController(value = detail(), query = '') {
  setup()
  return renderRoutes([{ path: '/', element: <TaskLarkSwitchController detail={value} node={value.nodes[1]} disabled={false} /> }], '/' + query)
}
async function chooseMembers() {
  const members = detail().larkSwitchParticipants!
  await userEvent.selectOptions(screen.getByLabelText(copy.choosePublisher), members[0].runtimeId)
  await userEvent.selectOptions(screen.getByLabelText(copy.chooseFollowup), members[1].runtimeId)
  await waitFor(() => expect(screen.getByRole('button', { name: copy.switch })).toBeEnabled())
  await userEvent.click(screen.getByRole('button', { name: copy.switch }))
  await userEvent.selectOptions(screen.getByLabelText(copy.target), account.id)
}
describe('formal ConsoleTaskDetail participants -> explicit switch submission', () => {
  it('does not auto-select even the only two instances; future unprepared member comes from projection, not nodes', async () => {
    const value = detail(); renderController(value)
    expect(screen.getByLabelText(copy.choosePublisher)).toHaveValue('')
    expect(screen.getByLabelText(copy.chooseFollowup)).toHaveValue('')
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    expect(value.nodes.some(n => n.runtimeId === value.larkSwitchParticipants![1].runtimeId)).toBe(false)
    await chooseMembers()
    expect(screen.getByText(new RegExp(copy.unprepared))).toBeVisible()
    expect(screen.getByRole('button', { name: copy.confirm })).toBeEnabled()
  })
  it('distinguishes absent capability from supported empty participants without guessing or posting', () => {
    const value = detail(); delete value.larkSwitchParticipants
    const first = renderController(value); expect(screen.getByText(copy.switchUnavailable)).toBeVisible(); first.unmount()
    renderController({ ...detail(), larkSwitchParticipants: [] })
    expect(screen.getByText(copy.noParticipants)).toBeVisible(); expect(screen.queryByText(copy.switchUnavailable)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
  })
  it.each([null, false] as const)('original permission %s cannot be selected even when static available says true', allowed => {
    const value = detail(); value.larkSwitchParticipants![1].larkAllowed = allowed
    renderController(value)
    expect(screen.getByRole('option', { name: new RegExp(allowed === null ? copy.permissionUnknown : copy.permissionDenied) })).toBeDisabled()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
  })
  it('unavailable members show the safe reason and never get selected automatically', () => {
    const value = detail(); value.larkSwitchParticipants![1].available = false; value.larkSwitchParticipants![1].unavailableReason = 'snapshot_invalid'
    renderController(value)
    expect(screen.getByRole('option', { name: /snapshot_invalid/ })).toBeDisabled()
  })
  it('commits original route before a single exact body, preserves future runtime and effective revisions', async () => {
    const bodies: Schemas['TaskLarkSwitchInput'][] = []
    const env = renderController()
    server.use(http.post<Record<string, never>, Schemas['TaskLarkSwitchInput']>(endpoint, async ({ request }) => {
      const body = await request.json(); bodies.push(body)
      expect(new URLSearchParams(env.router.state.location.search).get(taskLarkSwitchLocatorKey(taskId))).toBe(body.actionId)
      return HttpResponse.json({ ...larkSwitchReceipt('succeeded'), actionId: body.actionId }, { status: 202 })
    }), http.get(endpoint + '/:id', ({ params }) => HttpResponse.json({ ...larkSwitchReceipt('succeeded'), actionId: params.id })))
    await chooseMembers(); await userEvent.dblClick(screen.getByRole('button', { name: copy.confirm }))
    await screen.findByText(copy.committed)
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toEqual({ ...larkSwitchInput(), actionId: bodies[0].actionId })
    expect(screen.getByText(new RegExp(copy.runStatus.queued))).toBeVisible()
  })
  it.each([409, 503])('POST%s then GET404 keeps original selections and locator; reload never reposts', async status => {
    let posts = 0
    server.use(http.post(endpoint, () => { posts++; return HttpResponse.json({ error: { code: status === 409 ? 'task_conflict' : 'internal_error', message: 'Synthetic unavailable' } }, { status }) }), http.get(endpoint + '/:id', () => HttpResponse.json({ error: { code: 'not_found', message: 'Synthetic unknown action' } }, { status: 404 })))
    const env = renderController(); await chooseMembers(); await userEvent.click(screen.getByRole('button', { name: copy.confirm }))
    await screen.findByText(copy.notFound)
    expect(screen.getByLabelText(copy.target)).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
    const query = env.router.state.location.search; env.unmount(); renderController(detail(), query)
    await screen.findByText(copy.notFound); expect(posts).toBe(1)
    expect(screen.getByLabelText(copy.chooseFollowup)).toBeDisabled()
  })
  it('task version/binding changes during review preserve choice but disable confirmation', async () => {
    setup(); let update!: (value: Schemas['ConsoleTaskDetail']) => void
    function Page() { const [value, setValue] = useState(detail); update = setValue; return <TaskLarkSwitchController detail={value} node={value.nodes[1]} disabled={false} /> }
    renderRoutes([{ path: '/', element: <Page /> }], '/')
    await chooseMembers()
    const next = detail(); next.task.version++; next.larkSwitchParticipants![1].bindingRevision++
    act(() => update(next))
    expect(screen.getAllByText(copy.memberChanged).length).toBeGreaterThan(0)
    expect(screen.getByLabelText(copy.target)).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
  })
  it('shared SSE without console metadata preserves reviewed choices but blocks writes until fresh HTTP', async () => {
    setup(); let update!: (value: Schemas['ConsoleTaskDetail']) => void
    function Page() { const [value, setValue] = useState(detail); update = setValue; return <TaskLarkSwitchController detail={value} node={value.nodes[1]} disabled={false} /> }
    renderRoutes([{ path: '/', element: <Page /> }], '/')
    await chooseMembers()
    const snapshot = detail(); delete snapshot.larkSwitchParticipants
    act(() => update(snapshot))
    expect(screen.getByLabelText(copy.target)).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
    act(() => update(detail()))
    expect(screen.getByLabelText(copy.target)).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeEnabled()
    act(() => update({ ...detail(), larkSwitchParticipants: [] }))
    expect(screen.getByLabelText(copy.target)).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
  })
})
