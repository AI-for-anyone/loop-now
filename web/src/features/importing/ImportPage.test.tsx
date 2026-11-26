import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody, makeProject, projectsUrl } from '@/test/api'
import { makeImport, makeImportConversation, makeImportFolder, makeScan } from '@/test/importing'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeAgentOverview, renderOverview } from '@/test/overview'

const existing = makeProject({ id: '00000000-0000-7000-8000-0000000000b1', name: 'loop-now' })

// The scan answers running first, then each read gives the next state; the import likewise.
function backend({ scans, imports = [] }: { scans: Schemas['ConversationScan'][]; imports?: Schemas['ConversationImport'][] }) {
  const scanBodies: Schemas['ConversationScanInput'][] = []
  const importBodies: Schemas['ConversationImportInput'][] = []
  let scanRead = 0
  let importRead = 0
  server.use(
    ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject, existing] })),
    http.post(apiUrl('/conversation-scans'), async ({ request }) => {
      scanBodies.push((await request.json()) as Schemas['ConversationScanInput'])
      scanRead = 0
      return HttpResponse.json(scans[0], { status: 202 })
    }),
    http.get(apiUrl('/conversation-scans/:id'), () => HttpResponse.json(scans[Math.min(++scanRead, scans.length - 1)])),
    http.post(apiUrl('/conversation-imports'), async ({ request }) => {
      importBodies.push((await request.json()) as Schemas['ConversationImportInput'])
      importRead = 0
      return HttpResponse.json(imports[0], { status: 202 })
    }),
    http.get(apiUrl('/conversation-imports/:id'), () => HttpResponse.json(imports[Math.min(++importRead, imports.length - 1)])),
  )
  return { scanBodies, importBodies, scanReads: () => scanRead, importReads: () => importRead }
}

const t3 = makeImportFolder({
  path: '/code/t3code',
  repository: { remote: 'git@github.com:pingdotgg/t3code.git', mainCheckout: '/code/t3code' },
  conversations: [
    makeImportConversation({ sessionId: 'a', title: 'Welcome page', cwd: '/code/t3code' }),
    makeImportConversation({ sessionId: 'b', title: 'Permission modes', cli: 'codex', cwd: '/code/t3code/.worktrees/modes' }),
    makeImportConversation({ sessionId: 'c', title: 'Empty one', skipReason: 'empty' }),
  ],
})
const notes = makeImportFolder({
  path: '/code/loop-now',
  git: false,
  repository: null,
  preChecked: false,
  projectId: existing.id,
  conversations: [
    makeImportConversation({ sessionId: 'd', title: 'loop-now-main', cwd: '/code/loop-now' }),
    makeImportConversation({ sessionId: 'e', title: 'Release', cwd: '/code/loop-now', agentId: '00000000-0000-7000-8000-00000000aaaa' }),
  ],
})
const running = makeScan({ state: 'running', filesTotal: 316, filesDone: 128, folders: [] })
const found = makeScan({ folders: [t3, notes] })

afterEach(() => {
  window.localStorage.clear()
  window.sessionStorage.clear()
})

describe('The import page', () => {
  it('looks for the last 30 days, shows progress, then the suggested folders checked and the rest on asking', async () => {
    const { scanBodies } = backend({ scans: [running, running, found] })
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    expect(await screen.findByText('Read 128 of 316 history files')).toBeInTheDocument()
    expect(scanBodies).toHaveLength(1)
    const since = Date.parse(scanBodies[0].since!)
    expect(Date.now() - since).toBeGreaterThan(29.9 * 24 * 3600_000)
    expect(Date.now() - since).toBeLessThan(30.1 * 24 * 3600_000)

    const repo = await screen.findByRole('region', { name: 'pingdotgg/t3code' })
    expect(within(repo).getByRole('checkbox', { name: '/code/t3code' })).toBeChecked()
    // The suggested folder is open: what Import takes is in view, the empty conversation can't be picked, a worktree says where it is.
    expect(within(repo).getByRole('checkbox', { name: 'Welcome page' })).toBeChecked()
    expect(within(repo).getByRole('checkbox', { name: 'Empty one' })).toBeDisabled()
    expect(within(repo).getByText("Can't import: no conversation in it")).toBeInTheDocument()
    expect(within(repo).getByText('in /code/t3code/.worktrees/modes')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: /^Other folders/ })).toBeNull()
    expect(screen.getByText('1 folder · 2 conversations selected')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Review all 2 folders' }))
    const other = screen.getByRole('region', { name: /^Other folders/ })
    const folder = within(other).getByRole('checkbox', { name: '/code/loop-now' })
    expect(folder).not.toBeChecked()
    expect(within(other).getByText('Joins project “loop-now”')).toBeInTheDocument()
    await user.click(within(other).getByRole('button', { name: 'Show the conversations in /code/loop-now' }))
    // Already in Loop Now (imported before, or an Agent's own conversation): no check, a link to its Agent; never called imported.
    expect(within(other).getByRole('checkbox', { name: 'Release' })).toBeDisabled()
    expect(within(other).getByText('Already in Loop Now')).toBeInTheDocument()
    expect(within(other).getByRole('link', { name: 'Open Agent' })).toHaveAttribute('href', '/agents/00000000-0000-7000-8000-00000000aaaa/chat')
  })

  it('Import sends the picked conversations by folder, shows progress until it is done, then links each project', async () => {
    const importing = makeImport({ state: 'running', done: 1, total: 3 })
    const done = makeImport({ state: 'done', done: 3, total: 3, projects: [{ path: '/code/loop-now', projectId: existing.id }], agents: [{ sessionId: 'a', agentId: 'x' }, { sessionId: 'd', agentId: 'y' }], errors: [{ sessionId: 'b', message: 'Not enough disk space' }] })
    const api = backend({ scans: [found], imports: [importing, importing, done] })
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    await screen.findByRole('region', { name: 'pingdotgg/t3code' })
    await user.click(screen.getByRole('button', { name: 'Review all 2 folders' }))
    await user.click(screen.getByRole('checkbox', { name: '/code/loop-now' }))
    // Unpicking one conversation leaves its folder half checked.
    await user.click(screen.getByRole('checkbox', { name: 'Permission modes' }))
    expect(screen.getByRole('checkbox', { name: '/code/t3code' })).toHaveProperty('indeterminate', true)
    await user.click(screen.getByRole('button', { name: 'Import' }))

    await waitFor(() => expect(api.importBodies).toHaveLength(1))
    expect(api.importBodies[0]).toEqual({
      scanId: found.operationId,
      folders: [
        { path: '/code/t3code', projectId: null, sessionIds: ['a'] },
        { path: '/code/loop-now', projectId: existing.id, sessionIds: ['d'] },
      ],
    })
    expect(await screen.findByText('Importing 1 of 3')).toBeInTheDocument()
    expect(await screen.findByText('Import done')).toBeInTheDocument()
    expect(screen.getByText('2 Agents created')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open project “loop-now”' })).toHaveAttribute('href', '/p/loop-now/agents')
    expect(screen.getByText('Permission modes')).toBeInTheDocument()
    expect(screen.getByText(/Not enough disk space/)).toBeInTheDocument()
    // Reading stops once it is done.
    const reads = api.importReads()
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(api.importReads()).toBe(reads)
  })

  it('a failed scan says why and looks again on asking; All reads the whole history', async () => {
    const failed = makeScan({ state: 'failed', error: '~/.codex/sessions could not be read' })
    const api = backend({ scans: [failed] })
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    expect(await screen.findByText('~/.codex/sessions could not be read')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Look again' }))
    await waitFor(() => expect(api.scanBodies).toHaveLength(2))
    await user.click(screen.getByRole('radio', { name: 'All' }))
    await waitFor(() => expect(api.scanBodies).toHaveLength(3))
    expect(api.scanBodies[2].since).toBeNull()
  })

  it('from a project: only its folder, everything shown, and the import goes into that project', async () => {
    const api = backend({ scans: [makeScan({ folders: [notes] })], imports: [makeImport({ state: 'done' })] })
    const user = userEvent.setup()
    renderRoutes(routes, `/import?${new URLSearchParams({ path: '/code/loop-now', project: existing.id })}`)
    await waitFor(() => expect(api.scanBodies).toHaveLength(1))
    expect(api.scanBodies[0].path).toBe('/code/loop-now')
    expect(await screen.findByRole('checkbox', { name: 'loop-now-main' })).toBeChecked()
    await user.click(screen.getByRole('button', { name: 'Import' }))
    await waitFor(() => expect(api.importBodies[0]?.folders).toEqual([{ path: '/code/loop-now', projectId: existing.id, sessionIds: ['d'] }]))
  })
})

describe('The import page when things go wrong', () => {
  it('Import is sent once, however often it is clicked while it starts', async () => {
    backend({ scans: [found], imports: [makeImport({ state: 'done' })] })
    // The import takes a moment to start.
    let posts = 0
    let release = () => {}
    const gate = new Promise<void>((resolve) => (release = resolve))
    server.use(http.post(apiUrl('/conversation-imports'), async () => {
      posts += 1
      await gate
      return HttpResponse.json(makeImport({ state: 'done' }), { status: 202 })
    }))
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    const start = await screen.findByRole('button', { name: 'Import' })
    await user.dblClick(start)
    await user.click(start)
    expect(start).toBeDisabled()
    release()
    await screen.findByText('Import done')
    expect(posts).toBe(1)
  })

  it('a read of the import that fails stops reading, says so and can read again', async () => {
    let fail = true
    const reads: number[] = []
    backend({ scans: [found], imports: [makeImport({ state: 'running', done: 1, total: 2 })] })
    server.use(http.get(apiUrl('/conversation-imports/:id'), () => {
      reads.push(Date.now())
      return fail ? HttpResponse.json(errorBody('not_found', 'No such import.'), { status: 404 }) : HttpResponse.json(makeImport({ state: 'done', done: 2, total: 2 }))
    }))
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    await user.click(await screen.findByRole('button', { name: 'Import' }))
    expect(await screen.findByText(/can’t be read/)).toBeInTheDocument()
    const count = reads.length
    await new Promise((resolve) => setTimeout(resolve, 900))
    expect(reads).toHaveLength(count)
    fail = false
    await user.click(screen.getByRole('button', { name: 'Read it again' }))
    expect(await screen.findByText('Import done')).toBeInTheDocument()
  })

  it('a folder takes at most 4 GB in one import: a check picks the newest that fit', async () => {
    const big = (sessionId: string) => makeImportConversation({ sessionId, title: sessionId, sizeBytes: 1.5 * 1024 ** 3 })
    const heavy = makeImportFolder({ path: '/code/heavy', conversations: [big('one'), big('two'), big('three')] })
    const api = backend({ scans: [makeScan({ folders: [heavy] })], imports: [makeImport({ state: 'done' })] })
    const user = userEvent.setup()
    renderRoutes(routes, '/import')
    expect(await screen.findByRole('checkbox', { name: 'one' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'two' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'three' })).toBeDisabled()
    expect(screen.getByText(/One import takes at most 100 conversations and 4 GB/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Import' }))
    await waitFor(() => expect(api.importBodies[0]?.folders[0].sessionIds).toEqual(['one', 'two']))
  })
})

describe('Where the import page opens from', () => {
  it('a fresh install with no Agents opens it by itself, once: Skip goes back and it stays skipped', async () => {
    backend({ scans: [found] })
    const { snapshot, user } = await renderOverview()
    await snapshot([])
    expect(await screen.findByRole('heading', { name: 'Import conversations' })).toBeInTheDocument()
    expect(screen.getByText(/This is the first time Loop Now is open/)).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Skip for now' }))
    expect(window.localStorage.getItem('loop-now.importSkipped')).toBe('1')
    // Back on the overview (its stream starts again): still no Agents, and now the empty overview offers both ways in.
    await snapshot([])
    expect(await screen.findByRole('link', { name: 'Import conversations' })).toHaveAttribute('href', '/import')
  })

  it('on a fresh install it can be skipped while it is still looking, and it does not open by itself twice in one session', async () => {
    backend({ scans: [running] })
    const { snapshot, user } = await renderOverview()
    await snapshot([])
    expect(await screen.findByText('Looking for conversations…')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Skip for now' }))
    await snapshot([])
    expect(await screen.findByRole('link', { name: 'Import conversations' })).toBeInTheDocument()
  })

  it('once opened by itself, the overview does not send the person back to it in the same session', async () => {
    window.sessionStorage.setItem('loop-now.importShown', '1')
    backend({ scans: [found] })
    const { snapshot } = await renderOverview()
    await snapshot([])
    expect(await screen.findByRole('link', { name: 'Import conversations' })).toBeInTheDocument()
    expect(screen.queryByText(/This is the first time Loop Now is open/)).toBeNull()
    window.sessionStorage.clear()
  })

  it('with Agents the overview stays the overview', async () => {
    backend({ scans: [found] })
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview()])
    await screen.findByRole('region', { name: 'Run status' })
    expect(screen.queryByRole('heading', { name: 'Import conversations' })).toBeNull()
  })

  it('Settings opens it any time', async () => {
    server.use(...baseHandlers([]), http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{}', revision: 1 })))
    renderRoutes(routes, '/settings')
    expect(await screen.findByRole('link', { name: 'Open the import page' })).toHaveAttribute('href', '/import')
  })

  it('a project offers it for each of its local directories, for that folder and into that project', async () => {
    const { localModeBackend, dir } = await import('@/test/local-mode')
    const local = localModeBackend({ projects: { [defaultProject.id]: { revision: 1, directories: [dir('/Users/me/code/app', 'Main repo')] } } })
    server.use(...baseHandlers([]), ...local.handlers)
    renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/settings`)
    const link = await screen.findByRole('link', { name: 'Import the conversations in /Users/me/code/app' })
    expect(link).toHaveAttribute('href', `/import?${new URLSearchParams({ path: '/Users/me/code/app', project: defaultProject.id })}`)
  })
})
