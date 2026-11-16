import { useState } from 'react'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
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
// Redesign step 5: the form is tucked under the “更换 Lark 账号并继续 QA” (Switch Lark account and continue
// QA) button and collapsed by default; the test cases here open it first and then use the form.
function openSwitch() {
  const toggle = document.querySelector<HTMLButtonElement>('[data-lark-switch-toggle]')
  if (toggle && !toggle.disabled && toggle.getAttribute('aria-expanded') === 'false') fireEvent.click(toggle)
}
// The “更换 Lark 账号并继续 QA” (Switch Lark account and continue QA) button inside the form (it has the same name as the expand button above).
const switchButton = () => within(document.querySelector<HTMLElement>('[data-lark-switch-panel]')!).getByRole('button', { name: copy.switch })
function renderController(value = detail(), query = '') {
  setup()
  const result = renderRoutes([{ path: '/', element: <TaskLarkSwitchController detail={value} node={value.nodes[1]} disabled={false} /> }], '/' + query)
  openSwitch()
  return result
}
async function chooseMembers() {
  openSwitch()
  const members = detail().larkSwitchParticipants!
  await userEvent.selectOptions(screen.getByLabelText(copy.choosePublisher), members[0].runtimeId)
  await userEvent.selectOptions(screen.getByLabelText(copy.chooseFollowup), members[1].runtimeId)
  await waitFor(() => expect(switchButton()).toBeEnabled())
  await userEvent.click(switchButton())
  await userEvent.selectOptions(screen.getByLabelText(copy.target), account.id)
}
describe('formal ConsoleTaskDetail participants -> explicit switch submission', () => {
  it('does not auto-select even the only two instances; future unprepared member comes from projection, not nodes', async () => {
    const value = detail(); renderController(value)
    expect(screen.getByLabelText(copy.choosePublisher)).toHaveValue('')
    expect(screen.getByLabelText(copy.chooseFollowup)).toHaveValue('')
    expect(switchButton()).toBeDisabled()
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
    expect(switchButton()).toBeDisabled()
  })
  it.each([null, false] as const)('original permission %s cannot be selected even when static available says true', allowed => {
    const value = detail(); value.larkSwitchParticipants![1].larkAllowed = allowed
    renderController(value)
    expect(screen.getByRole('option', { name: new RegExp(allowed === null ? copy.permissionUnknown : copy.permissionDenied) })).toBeDisabled()
    expect(switchButton()).toBeDisabled()
  })
  it('unavailable members show the safe reason and never get selected automatically', () => {
    const value = detail(); value.larkSwitchParticipants![1].available = false; value.larkSwitchParticipants![1].unavailableReason = 'snapshot_invalid'
    renderController(value)
    expect(screen.getByRole('option', { name: /snapshot_invalid/ })).toBeDisabled()
  })
  it('a known unavailable reason is named in the UI language', () => {
    const value = detail(); value.larkSwitchParticipants![1].available = false; value.larkSwitchParticipants![1].unavailableReason = 'session_unavailable'
    renderController(value)
    expect(screen.getByRole('option', { name: /The instance's session isn't available/ })).toBeDisabled()
    expect(screen.queryByRole('option', { name: /session_unavailable/ })).toBeNull()
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

describe('placement: only a secondary button in the node header, and the form opens when it is clicked', () => {
  const render = (value = detail(), query = '') => {
    setup()
    return renderRoutes([{ path: '/', element: <TaskLarkSwitchController detail={value} node={value.nodes[1]} disabled={false} /> }], '/' + query)
  }
  const toggle = () => screen.getByRole('button', { name: copy.switch, expanded: false })
  it('collapsed by default: only the button (aria-expanded=false, aria-controls pointing to the collapsed panel), with no member choice in sight', () => {
    render()
    const button = toggle()
    expect(button).toBeEnabled()
    const panel = document.getElementById(button.getAttribute('aria-controls')!)!
    expect(panel).toHaveAttribute('hidden')
    expect(screen.queryByRole('combobox', { name: copy.choosePublisher })).not.toBeInTheDocument()
  })
  it('clicking it opens the original form below; clicking again collapses it', async () => {
    render()
    await userEvent.click(toggle())
    const button = screen.getByRole('button', { name: copy.switch, expanded: true })
    expect(document.getElementById(button.getAttribute('aria-controls')!)).not.toHaveAttribute('hidden')
    expect(screen.getByRole('combobox', { name: copy.choosePublisher })).toBeVisible()
    await userEvent.click(button)
    expect(toggle()).toBeInTheDocument()
  })
  it('capability not ready: the button is disabled with the reason as one line of small text beside it, no card', () => {
    const value = detail(); delete value.larkSwitchParticipants
    render(value)
    expect(toggle()).toBeDisabled()
    expect(toggle().parentElement).toHaveTextContent(copy.switchUnavailable)
    expect(screen.queryByRole('region', { name: copy.switch })).not.toBeInTheDocument()
  })
  it('with an original action ID: without opening it, the original action and “Check the original operation” are visible right away', async () => {
    server.use(http.get(endpoint + '/:id', ({ params }) => HttpResponse.json({ ...larkSwitchReceipt('succeeded'), actionId: params.id })))
    const id = '30000000-0000-7000-8000-000000000009'
    render(detail(), `?${taskLarkSwitchLocatorKey(taskId)}=${id}`)
    const button = screen.getByRole('button', { name: copy.switch, expanded: true })
    expect(document.getElementById(button.getAttribute('aria-controls')!)).not.toHaveAttribute('hidden')
    expect(screen.getByText(new RegExp(id))).toBeVisible()
    expect(screen.getByRole('button', { name: copy.recover })).toBeVisible()
    expect(await screen.findByText(copy.committed)).toBeVisible()
  })
})
