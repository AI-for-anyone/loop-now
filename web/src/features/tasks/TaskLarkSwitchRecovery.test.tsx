import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { taskId } from '@/test/tasks'
import { larkSwitchReceipt, switchActionId } from '@/test/lark-switch'
import { taskLarkSwitchLocatorKey } from '@/lib/useOriginalActionLocator'
import { larkText as copy } from '@/i18n/lark'
import { TaskLarkSwitchRecovery } from './TaskLarkSwitchRecovery'

const key = taskLarkSwitchLocatorKey(taskId), endpoint = apiUrl(`/tasks/${taskId}/lark-account-switches`)
function renderRecovery(id?: string, currentBlocked = true) {
  return renderRoutes([{ path: '/', element: <TaskLarkSwitchRecovery taskId={taskId} currentBlocked={currentBlocked} /> }], id ? `/?${key}=${id}` : '/')
}
describe('original task action recovery without inferred participants', () => {
  it('without participants exposes a disabled action, no guessed POST or GET', () => {
    renderRecovery()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    expect(screen.getByText(copy.switchUnavailable)).toBeVisible()
    expect(screen.queryByRole('button', { name: copy.recover })).not.toBeInTheDocument()
  })
  it('original ID GET404 remains unconfirmed across reload/manual read, never POST', async () => {
    let gets = 0, posts = 0
    server.use(http.get(endpoint + '/:id', ({ params }) => { expect(params.id).toBe(switchActionId); gets++; return HttpResponse.json({ error: { code: 'not_found', message: 'Synthetic action missing' } }, { status: 404 }) }), http.post(endpoint, () => { posts++; return HttpResponse.json({}) }))
    const first = renderRecovery(switchActionId)
    await screen.findByText(copy.notFound)
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    await waitFor(() => expect(gets).toBe(2))
    first.unmount(); const next = renderRecovery(switchActionId)
    await screen.findByText(copy.notFound)
    expect(new URLSearchParams(next.router.state.location.search).get(key)).toBe(switchActionId)
    expect(gets).toBe(3); expect(posts).toBe(0)
  })
  it('recovers the old original action on a historical/new-node view, keeps receipt separate from dispatch/QA', async () => {
    const value = larkSwitchReceipt('succeeded')
    if (value.status !== 'succeeded') throw new Error('Synthetic successful branch required')
    server.use(http.get(endpoint + '/:id', () => HttpResponse.json({ ...value, dispatchStatus: 'failed' })))
    renderRecovery(switchActionId, false)
    await screen.findByText(copy.committed); expect(screen.getByText(copy.dispatchFailed)).toBeVisible()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    await userEvent.click(screen.getByText(copy.receiptDetails))
    expect(screen.getByText(new RegExp(value.target.versionId))).toBeVisible()
    expect(screen.getByText(new RegExp(value.continuation.nodeRunId))).toBeVisible()
  })
  it('invalid original locator is not replaced or used for requests', () => {
    const env = renderRecovery('invalid')
    expect(screen.getByText(copy.invalidLocator)).toBeVisible()
    expect(screen.queryByRole('button', { name: copy.recover })).not.toBeInTheDocument()
    expect(new URLSearchParams(env.router.state.location.search).get(key)).toBe('invalid')
  })
})
