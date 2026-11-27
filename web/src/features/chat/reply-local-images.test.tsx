import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { agentFolderRawURL, taskNodeFolderRawURL } from '@/api/folders'
import { tasksPath } from '@/api/tasks'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeMessage, makeRun, projectsUrl } from '@/test/api'
import { renderChat } from '@/test/chat'
import { agentParty, makeInboxMessage, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, makeTaskMessage, nodeId, taskId } from '@/test/tasks'

const PNG = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><script>window.__ran=true</script><circle cx="20" cy="20" r="15"/></svg>'

const entry = (path: string, type: Schemas['FolderEntry']['type'], size: number | null = type === 'file' ? 64 : null): Schemas['FolderEntry'] => ({
  name: path.split('/').at(-1)!,
  path,
  type,
  size,
  modifiedAt: '2026-11-26T08:00:00Z',
  symlinkTarget: null,
})

describe('Local folder links and images in regular chat, task nodes, and Inbox', () => {
  it('opens local files in a new sandboxed tab, keeps image thumbnails, navigates directory links to the Folder tab, and blocks outside/missing paths in regular chat', async () => {
    const agent = makeAgent({ name: 'designer' })
    const root = `/var/work/agents/${agent.id}/workspace`
    const realRoot = `/private/var/work/agents/${agent.id}/workspace`
    const run = makeRun()
    const reply = [
      '![Banner](assets/banner.png)',
      '',
      `Animation: [Open Pelican](${root}/pelican-night-ride.html) · [Relative](pelican-night-ride.html) · \`pelican-night-ride.html\``,
      '',
      `SVG concept: \`${realRoot}/assets/mascot/concept.svg\``,
      '',
      `Directory: [Mascot folder](${root}/assets/mascot/)`,
      '',
      'Outside: [Secret](/etc/passwd) and `/etc/secret.png`. Missing: [Deleted](gone.html).',
    ].join('\n')

    const { user, router } = await renderChat({
      agent,
      messages: [makeMessage({ text: 'Show me the draft', run }), makeMessage({ role: 'assistant', text: reply, run })],
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/folder`), () => HttpResponse.json({ path: root, realPath: realRoot })),
        http.get(apiUrl(`/agents/${agent.id}/folder/content`), ({ request }) => {
          const rel = new URL(request.url).searchParams.get('path')
          if (rel === 'assets/banner.png') return new HttpResponse(PNG, { headers: { 'Content-Type': 'application/octet-stream' } })
          if (rel === 'assets/mascot/concept.svg') return new HttpResponse(SVG, { headers: { 'Content-Type': 'application/octet-stream' } })
          return new HttpResponse(null, { status: 404 })
        }),
        http.head(apiUrl(`/agents/${agent.id}/folder/raw/*`), ({ request }) => {
          const rel = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
          if (rel === 'pelican-night-ride.html') return new HttpResponse(null, { status: 200, headers: { 'Content-Length': '18432' } })
          if (rel === 'assets/banner.png' || rel === 'assets/mascot/concept.svg') {
            return new HttpResponse(null, { status: 200 })
          }
          if (rel === 'assets/mascot') return new HttpResponse(null, { status: 422 })
          return new HttpResponse(null, { status: 404 })
        }),
        http.get(apiUrl(`/agents/${agent.id}/folder/entries`), ({ request }) => {
          const dir = new URL(request.url).searchParams.get('path') ?? ''
          if (dir === '') return HttpResponse.json({ path: '', entries: [entry('assets', 'dir'), entry('pelican-night-ride.html', 'file', 18432)], nextCursor: null })
          if (dir === 'assets') return HttpResponse.json({ path: 'assets', entries: [entry('assets/mascot', 'dir'), entry('assets/banner.png', 'file')], nextCursor: null })
          if (dir === 'assets/mascot') return HttpResponse.json({ path: 'assets/mascot', entries: [entry('assets/mascot/concept.svg', 'file', SVG.length)], nextCursor: null })
          return new HttpResponse(null, { status: 404 })
        }),
      ],
    })

    // Both thumbnails render from the folder content endpoint; clicking enlarges in the shared preview dialog.
    expect(await screen.findByRole('button', { name: 'Enlarge image: Banner' })).toBeInTheDocument()
    const svgButton = await screen.findByRole('button', { name: 'Enlarge image: concept.svg' })
    expect(within(svgButton).getByRole('img', { name: 'concept.svg' }).tagName).toBe('IMG')

    // Every inside file link opens the sandboxed raw view in a new tab (never navigates the console).
    for (const [name, rel] of [
      ['Open Pelican', 'pelican-night-ride.html'],
      ['Relative', 'pelican-night-ride.html'],
      ['pelican-night-ride.html', 'pelican-night-ride.html'],
      [`${realRoot}/assets/mascot/concept.svg`, 'assets/mascot/concept.svg'],
    ] as const) {
      const link = await screen.findByRole('link', { name })
      expect(link, name).toHaveAttribute('href', agentFolderRawURL(agent.id, rel))
      expect(link, name).toHaveAttribute('target', '_blank')
      expect(link, name).toHaveAttribute('rel', 'noopener noreferrer')
    }

    // Outside and missing paths are non-clickable text with explanatory tooltips.
    expect(screen.queryByRole('link', { name: 'Secret' })).toBeNull()
    expect(screen.queryByRole('link', { name: '/etc/secret.png' })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Deleted' })).toBeNull()
    expect(screen.getByText('Secret').closest('[title]')).toHaveAttribute('title', 'Outside this folder; cannot be opened')
    expect(screen.getByText('/etc/secret.png').closest('[title]')).toHaveAttribute('title', 'Outside this folder; cannot be opened')
    await waitFor(() => expect(screen.getByText('Deleted').closest('[title]')).toHaveAttribute('title', 'This file no longer exists in the folder'))

    // Non-image files produce file cards (missing file shows a gray card; images keep thumbnails without cards).
    expect(await screen.findByRole('link', { name: 'pelican-night-ride.html, Web page · 18 KB, open in new tab' })).toHaveAttribute(
      'href',
      agentFolderRawURL(agent.id, 'pelican-night-ride.html'),
    )
    expect(document.querySelector('[data-md-file-card="gone.html"]')).toHaveAttribute('data-md-file-card-state', 'missing')
    expect(document.querySelector('[data-md-file-card="assets/banner.png"]')).toBeNull()
    expect(document.querySelector('[data-md-file-card="assets/mascot/concept.svg"]')).toBeNull()

    await user.click(svgButton)
    const dialog = await screen.findByRole('dialog', { name: 'concept.svg' })
    expect(within(dialog).getByRole('img', { name: 'concept.svg' }).tagName).toBe('IMG')
    await user.keyboard('{Escape}')

    // Clicking a directory link keeps navigating in-app to the Agent's Folder tab.
    await user.click(await screen.findByRole('link', { name: 'Mascot folder' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(new RegExp(`/agents/${agent.id}/folder$`)))
    expect(new URLSearchParams(router.state.location.search).get('path')).toBe('assets/mascot')
    const tree = await screen.findByRole('tree')
    await waitFor(() => expect(tree.querySelector('[data-path="assets/mascot"]')).toHaveAttribute('aria-selected', 'true'))
  })

  it('opens task node folder files in a new tab while active and falls back to non-clickable text once reclaimed (404)', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    const instanceRoot = `/work/tasks/${taskId}/instances/${detail.nodes[1].runtimeId}/workspace`
    const baseAgentRoot = `/work/agents/${detail.nodes[1].agentId}/workspace`
    const requested: string[] = []
    let reclaimed = false

    server.use(
      ...baseHandlers([]),
      http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
      http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
      http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) =>
        HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: detail.nodes[1].agentId, runtimeId: detail.nodes[1].runtimeId, canSend: false, reason: 'Blocked', handlingHint: '' }),
      ),
      http.get(apiUrl(`/tasks/${taskId}/nodes/:node/folder`), () => HttpResponse.json({ path: instanceRoot, realPath: instanceRoot })),
      http.get(apiUrl(`/tasks/${taskId}/nodes/:node/folder/content`), ({ request }) => {
        const rel = new URL(request.url).searchParams.get('path') ?? ''
        requested.push(rel)
        if (reclaimed) return new HttpResponse(null, { status: 404 })
        if (rel === 'shots/check.png') return new HttpResponse(PNG, { headers: { 'Content-Type': 'application/octet-stream' } })
        if (rel === 'shots/chart.svg') return new HttpResponse(SVG, { headers: { 'Content-Type': 'application/octet-stream' } })
        return new HttpResponse(null, { status: 404 })
      }),
      http.head(apiUrl(`/tasks/${taskId}/nodes/:node/folder/raw/*`), ({ request }) => {
        if (reclaimed) return new HttpResponse(null, { status: 404 })
        const rel = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
        if (rel === 'report/index.html' || rel === 'shots/check.png' || rel === 'shots/chart.svg') {
          return new HttpResponse(null, { status: 200 })
        }
        return new HttpResponse(null, { status: 404 })
      }),
      http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params }) => {
        const node = String(params.node)
        const text =
          node === nodeId
            ? [
                '![Check](shots/check.png)',
                '',
                `Report: [Open report](${instanceRoot}/report/index.html)`,
                '',
                `Instance SVG: \`${instanceRoot}/shots/chart.svg\``,
                '',
                `Base Agent path ignored: \`${baseAgentRoot}/shots/other.png\``,
              ].join('\n')
            : 'Earlier reply'
        return HttpResponse.json({ messages: [makeTaskMessage(node, text)], nextCursor: null })
      }),
    )

    const first = renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
    expect(await screen.findByRole('button', { name: 'Enlarge image: Check' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Enlarge image: chart.svg' })).toBeInTheDocument()
    expect(requested.sort()).toEqual(['shots/chart.svg', 'shots/check.png'])

    const reportLink = await screen.findByRole('link', { name: 'Open report' })
    expect(reportLink).toHaveAttribute('href', taskNodeFolderRawURL(taskId, nodeId, 'report/index.html'))
    expect(reportLink).toHaveAttribute('target', '_blank')
    expect(reportLink).toHaveAttribute('rel', 'noopener noreferrer')

    const svgLink = await screen.findByRole('link', { name: `${instanceRoot}/shots/chart.svg` })
    expect(svgLink).toHaveAttribute('href', taskNodeFolderRawURL(taskId, nodeId, 'shots/chart.svg'))
    expect(svgLink).toHaveAttribute('target', '_blank')

    // Base Agent path is outside the task instance folder and stays non-clickable.
    expect(screen.queryByRole('link', { name: `${baseAgentRoot}/shots/other.png` })).toBeNull()
    expect(screen.getByText(`${baseAgentRoot}/shots/other.png`).closest('[title]')).toHaveAttribute('title', 'Outside this folder; cannot be opened')
    first.unmount()

    // Once the task ends and its instance folder is reclaimed (404), links and thumbnails are omitted and missing tooltip is shown.
    reclaimed = true
    const second = renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
    expect(await screen.findByText(`${instanceRoot}/shots/chart.svg`)).toBeInTheDocument()
    await waitFor(() => expect(requested.length).toBeGreaterThanOrEqual(4))
    await waitFor(() => expect(screen.getByText('Open report').closest('[title]')).toHaveAttribute('title', 'This file no longer exists in the folder'))
    expect(screen.queryByRole('link', { name: 'Open report' })).toBeNull()
    expect(screen.queryByRole('link', { name: `${instanceRoot}/shots/chart.svg` })).toBeNull()
    expect(second.container.querySelector('[data-md-image]')).toBeNull()
    expect(second.container.querySelector('[data-md-image-error]')).toBeNull()
    expect(second.container.querySelector('img')).toBeNull()
  })

  it('opens local files from the sender Agent folder in a new tab inside the read-only Inbox', async () => {
    const receiver = makeAgent({ name: 'Reviewer', address: 'test-org/test-project/Reviewer' })
    const sender = makeAgent({ name: 'Builder', address: 'test-org/test-project/Builder' })
    const senderRoot = `/var/work/agents/${sender.id}/workspace`
    const message = makeInboxMessage({
      from: agentParty(sender),
      to: agentParty(receiver),
      text: `Demo ready: [Open demo](${senderRoot}/demo/index.html) and \`demo/index.html\`, outside [Host](/etc/hosts).`,
    })

    server.use(
      ...baseHandlers([receiver, sender]),
      http.get(apiUrl(`/agents/${receiver.id}/inbox`), () => HttpResponse.json({ messages: [summaryOf(message)], nextCursor: null })),
      http.get(apiUrl(`/agents/${receiver.id}/inbox/${message.id}`), () => HttpResponse.json(message)),
      http.get(apiUrl(`/agents/${sender.id}/folder`), () => HttpResponse.json({ path: senderRoot, realPath: senderRoot })),
      http.head(apiUrl(`/agents/${sender.id}/folder/raw/*`), ({ request }) => {
        const rel = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
        return rel === 'demo/index.html' ? new HttpResponse(null, { status: 200 }) : new HttpResponse(null, { status: 404 })
      }),
    )

    const user = userEvent.setup()
    renderRoutes(routes, `${defaultAgentsPath}/${receiver.id}/inbox`)
    const list = within(await screen.findByRole('list', { name: 'Messages received by Reviewer' }))
    await user.click(list.getByRole('button', { name: /Builder/ }))

    const demoLink = await list.findByRole('link', { name: 'Open demo' })
    expect(demoLink).toHaveAttribute('href', agentFolderRawURL(sender.id, 'demo/index.html'))
    expect(demoLink).toHaveAttribute('target', '_blank')
    expect(demoLink).toHaveAttribute('rel', 'noopener noreferrer')

    const inlineLink = await list.findByRole('link', { name: 'demo/index.html' })
    expect(inlineLink).toHaveAttribute('href', agentFolderRawURL(sender.id, 'demo/index.html'))
    expect(inlineLink).toHaveAttribute('target', '_blank')

    expect(list.queryByRole('link', { name: 'Host' })).toBeNull()
    expect(list.getByText('Host').closest('[title]')).toHaveAttribute('title', 'Outside this folder; cannot be opened')
  })
})
