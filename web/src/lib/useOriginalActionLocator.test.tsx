import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { larkLoginLocatorKey, taskLarkSwitchLocatorKey, useOriginalActionLocator } from './useOriginalActionLocator'

const original = '30000000-0000-7000-8000-000000000001'
const key = larkLoginLocatorKey('project-one')
function Probe({ invoke, scope = key }: { invoke: (id: string) => Promise<void>; scope?: string }) {
  const action = useOriginalActionLocator(scope), location = useLocation()
  return <><p data-testid="location">{location.search}</p><p data-testid="id">{action.id}</p><p data-testid="valid">{String(action.valid)}</p><p data-testid="error">{action.error instanceof Error ? action.error.message : ''}</p><button onClick={() => action.begin(invoke)}>开始</button><button onClick={() => action.begin(invoke, true)}>已核终态后新建</button><button onClick={() => action.begin(async id => { await invoke(id); action.rejectBeforeAcceptance(id) }, true)}>明确拒绝</button></>
}
describe('original action route commitment, no transport schema', () => {
  it('commits non-secret scoped ID before invoking a start; click reentry invokes once', async () => {
    let finish!: () => void
    const invoke = vi.fn((id: string) => {
      expect(new URLSearchParams(screen.getByTestId('location').textContent ?? '').get(key)).toBe(id)
      return new Promise<void>(resolve => { finish = resolve })
    })
    render(<MemoryRouter><Probe invoke={invoke} /></MemoryRouter>)
    await userEvent.dblClick(screen.getByText('开始'))
    expect(invoke).toHaveBeenCalledOnce()
    await act(async () => finish())
    expect(screen.getByTestId('location').textContent).not.toMatch(/token|code|secret|https/)
  })
  it('reload with an ID never invokes start, even on another click', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined)
    render(<MemoryRouter initialEntries={[`/?${key}=${original}`]}><Probe invoke={invoke} /></MemoryRouter>)
    expect(screen.getByTestId('id')).toHaveTextContent(original)
    await userEvent.click(screen.getByText('开始'))
    expect(invoke).not.toHaveBeenCalled()
  })
  it('unknown rejection retains original locator; remount still performs no automatic POST', async () => {
    const invoke = vi.fn().mockRejectedValue(new Error('unknown'))
    const view = render(<MemoryRouter><Probe invoke={invoke} /></MemoryRouter>)
    await userEvent.click(screen.getByText('开始'))
    await waitFor(() => expect(screen.getByTestId('error')).toHaveTextContent('unknown'))
    const search = screen.getByTestId('location').textContent ?? ''
    await userEvent.click(screen.getByText('开始')); expect(invoke).toHaveBeenCalledOnce()
    view.unmount()
    render(<MemoryRouter initialEntries={[`/${search}`]}><Probe invoke={invoke} /></MemoryRouter>)
    expect(invoke).toHaveBeenCalledOnce()
  })
  it('invalid route ID is not silently replaced, even via new-after-terminal helper', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined)
    render(<MemoryRouter initialEntries={[`/?${key}=invalid`]}><Probe invoke={invoke} /></MemoryRouter>)
    expect(screen.getByTestId('valid')).toHaveTextContent('false')
    await userEvent.click(screen.getByText('已核终态后新建'))
    expect(invoke).not.toHaveBeenCalled()
    expect(screen.getByTestId('id')).toHaveTextContent('invalid')
  })
  it('explicitly verified terminal allows an independent ID; all scopes have distinct keys', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined)
    render(<MemoryRouter initialEntries={[`/?${key}=${original}`]}><Probe invoke={invoke} /></MemoryRouter>)
    await userEvent.click(screen.getByText('已核终态后新建'))
    expect(invoke).toHaveBeenCalledOnce()
    expect(invoke.mock.calls[0][0]).not.toBe(original)
    expect(new Set([key, larkLoginLocatorKey('project-two'), larkLoginLocatorKey('project-one', 'account-one'), taskLarkSwitchLocatorKey('task-one')]).size).toBe(4)
  })
  it('definitive POST rejection uses the newly committed route, never restores the old terminal ID or loses other scopes', async () => {
    const invoke = vi.fn().mockResolvedValue(undefined)
    render(<MemoryRouter initialEntries={[`/?${key}=${original}&otherScope=keep`]}><Probe invoke={invoke} /></MemoryRouter>)
    await userEvent.click(screen.getByText('明确拒绝'))
    await waitFor(() => expect(screen.getByTestId('id')).toBeEmptyDOMElement())
    expect(invoke).toHaveBeenCalledOnce()
    const params = new URLSearchParams(screen.getByTestId('location').textContent ?? '')
    expect(params.get('otherScope')).toBe('keep')
    expect(params.has(key)).toBe(false)
  })
})
