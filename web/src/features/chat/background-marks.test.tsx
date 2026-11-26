import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeBackgroundTask, makeProject, makeRun, makeStatus, toListItem } from '@/test/api'
import { projectBackend } from '@/test/projects'
import { renderChat } from '@/test/chat'
import { makeAgentOverview, renderOverview } from '@/test/overview'
import { renderRoutes } from '@/test/render'
import { withAllRuntimes } from '@/test/runtimes'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

function snapshot(agentId: string, status: Partial<Schemas['AgentStatus']>) {
  return { status: makeStatus(agentId, status), currentText: null, queuedRuns: [], queueItems: [] }
}

describe('An idle Agent whose background tasks still run is marked in the lists', () => {
  it('the sidebar row says “Bg N” instead of nothing; replying still comes first', async () => {
    const idle = makeAgent({ name: 'Builder', backgroundTaskCount: 2 })
    const busy = makeAgent({ name: 'Runner', busy: true, backgroundTaskCount: 1 })
    const quiet = makeAgent({ name: 'Quiet' })
    server.use(http.get(agentsUrl, () => HttpResponse.json({ agents: [idle, busy, quiet].map((agent) => toListItem(agent)) })), ...baseHandlers([idle, busy, quiet]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    const row = (name: string) => within(list).getByRole('link', { name: new RegExp(name) })
    await waitFor(() => expect(row('Builder')).toHaveTextContent('Bg 2'))
    expect(within(row('Builder')).getByText('Bg 2')).toHaveAttribute('title', 'Idle; 2 background tasks are still running')
    expect(row('Runner')).toHaveTextContent('Replying')
    expect(row('Runner')).not.toHaveTextContent('Bg')
    expect(row('Quiet')).not.toHaveTextContent('Bg')
  })

  it('the overview row shows “Background N” next to an idle state, and not next to a busy one', async () => {
    const { snapshot: send } = await renderOverview()
    const running = [makeBackgroundTask(), makeBackgroundTask()]
    const idle = makeAgentOverview({ name: 'Builder', status: makeStatus('a', { backgroundTasks: running }) })
    const busy = makeAgentOverview({ name: 'Runner', status: makeStatus('b', { busy: true, currentRun: makeRun({ status: 'running', endedAt: null }), backgroundTasks: running }) })
    await send([idle, busy])
    const table = await screen.findByRole('table', { name: /./ })
    const builder = within(table).getByText('Builder').closest('tr') as HTMLElement
    const runner = within(table).getByText('Runner').closest('tr') as HTMLElement
    expect(within(builder).getByText('Background 2')).toBeInTheDocument()
    expect(within(runner).queryByText(/^Background/)).toBeNull()
  })
})

describe('The project Manager', () => {
  it('an idle Manager whose background tasks still run says so next to its state', async () => {
    const project = makeProject({ name: 'model-access' })
    const manager = makeAgent({ name: 'Lead', projectId: project.id, isManager: true, backgroundTaskCount: 1 })
    server.use(...projectBackend({ projects: [defaultProject, { ...project, managerAgentId: manager.id }], agents: [manager] }).handlers)
    renderRoutes(routes, '/p/model-access/settings')
    const card = await screen.findByRole('region', { name: 'Manager' })
    expect(await within(card).findByText('Background 1')).toBeInTheDocument()
    expect(within(card).getByText('Idle')).toBeInTheDocument()
  })
})

describe('What the message box says first', () => {
  it('when a settings change restarts the CLI with the next message, one line above the box says it stops the running tasks', async () => {
    const tasks = [makeBackgroundTask(), makeBackgroundTask()]
    const { agent, emit } = await renderChat({ agent: makeAgent({ runtime: 'claude-code', model: { provider: 'claude-code', modelId: 'sonnet' } }), status: { backgroundTasks: tasks, restartOnNextMessage: true } })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: tasks, restartOnNextMessage: true }))
    expect(await screen.findByText('Settings changed: the next message restarts Claude Code and stops 2 running background tasks.')).toBeInTheDocument()

    // Once the tasks have ended there is nothing to stop, and the line goes.
    await emit('background_tasks.updated', { tasks: [] })
    await waitFor(() => expect(screen.queryByText(/the next message restarts/)).toBeNull())
  })

  it('no line without a restart pending', async () => {
    const tasks = [makeBackgroundTask()]
    const { agent, emit } = await renderChat({ status: { backgroundTasks: tasks } })
    await emit('snapshot', snapshot(agent.id, { backgroundTasks: tasks }))
    await screen.findByRole('button', { name: /open to see or stop them/ })
    expect(screen.queryByText(/the next message restarts/)).toBeNull()
  })

  it('on a runtime whose stop ends its process, Stop says it also stops the background tasks', async () => {
    const grok = makeAgent({ runtime: 'grok', model: { provider: 'grok-cli', modelId: 'grok-4' } })
    const running = makeRun({ status: 'running', endedAt: null })
    const tasks = [makeBackgroundTask({ runtime: 'grok' })]
    const { emit } = await renderChat({ agent: grok, handlers: [http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withAllRuntimes()))] })
    await emit('snapshot', snapshot(grok.id, { busy: true, currentRun: running, backgroundTasks: tasks }))
    expect(await screen.findByText('Stopping or interrupting also stops 1 background task')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Stop (also stops 1 background task)' })).toBeInTheDocument()
    // The usual hint would say the opposite.
    expect(screen.queryByText('Stop the current reply only')).toBeNull()
  })

  it('where stopping a reply leaves the background tasks alone, nothing more is said', async () => {
    const claude = makeAgent({ runtime: 'claude-code', model: { provider: 'claude-code', modelId: 'sonnet' } })
    const running = makeRun({ status: 'running', endedAt: null })
    const { emit } = await renderChat({ agent: claude, handlers: [http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withAllRuntimes()))] })
    await emit('snapshot', snapshot(claude.id, { busy: true, currentRun: running, backgroundTasks: [makeBackgroundTask()] }))
    expect(await screen.findByRole('button', { name: 'Stop the current reply' })).toBeInTheDocument()
    expect(screen.queryByText(/also stops/)).toBeNull()
  })
})
