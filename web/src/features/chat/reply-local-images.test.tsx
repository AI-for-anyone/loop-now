import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { tasksPath } from '@/api/tasks'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, makeAgent, makeMessage, makeRun, projectsUrl } from '@/test/api'
import { renderChat } from '@/test/chat'
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

describe('Local folder images in regular chat and task node conversations', () => {
  it('renders local PNG and SVG thumbnails in regular chat and opens the clicked path in the Agent Folder tab', async () => {
    const agent = makeAgent({ name: 'designer' })
    const root = `/var/work/agents/${agent.id}/workspace`
    const realRoot = `/private/var/work/agents/${agent.id}/workspace`
    const run = makeRun()
    const reply = [
      '![Banner](assets/banner.png)',
      '',
      `SVG concept: \`${realRoot}/assets/mascot/concept.svg\``,
      '',
      'Outside path stays plain: `/etc/secret.png`.',
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
        http.get(apiUrl(`/agents/${agent.id}/folder/entries`), ({ request }) => {
          const dir = new URL(request.url).searchParams.get('path') ?? ''
          if (dir === '') return HttpResponse.json({ path: '', entries: [entry('assets', 'dir')], nextCursor: null })
          if (dir === 'assets') return HttpResponse.json({ path: 'assets', entries: [entry('assets/mascot', 'dir'), entry('assets/banner.png', 'file')], nextCursor: null })
          if (dir === 'assets/mascot') return HttpResponse.json({ path: 'assets/mascot', entries: [entry('assets/mascot/concept.svg', 'file', SVG.length)], nextCursor: null })
          return HttpResponse.json({ path: dir, entries: [], nextCursor: null })
        }),
      ],
    })

    // Both thumbnails render from the folder content endpoint; clicking enlarges in the shared preview dialog.
    expect(await screen.findByRole('button', { name: 'Enlarge image: Banner' })).toBeInTheDocument()
    const svgButton = await screen.findByRole('button', { name: 'Enlarge image: concept.svg' })
    expect(within(svgButton).getByRole('img', { name: 'concept.svg' }).tagName).toBe('IMG')
    expect(screen.queryByRole('link', { name: '/etc/secret.png' })).toBeNull()

    await user.click(svgButton)
    const dialog = await screen.findByRole('dialog', { name: 'concept.svg' })
    expect(within(dialog).getByRole('img', { name: 'concept.svg' }).tagName).toBe('IMG')
    await user.keyboard('{Escape}')

    // Clicking the path text opens the file directly in the Agent's Folder tab.
    await user.click(screen.getByRole('link', { name: `${realRoot}/assets/mascot/concept.svg` }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(new RegExp(`/agents/${agent.id}/folder$`)))
    expect(new URLSearchParams(router.state.location.search).get('path')).toBe('assets/mascot/concept.svg')
    const folderImg = await screen.findByRole('img', { name: 'concept.svg' })
    expect(folderImg.tagName).toBe('IMG')
    const tree = screen.getByRole('tree')
    expect(tree.querySelector('[data-path="assets/mascot/concept.svg"]')).toHaveAttribute('aria-selected', 'true')
  })

  it('loads task node reply thumbnails from the task instance folder only and shows no thumbnail once reclaimed (404)', async () => {
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
      http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params }) => {
        const node = String(params.node)
        const text =
          node === nodeId
            ? [
                '![Check](shots/check.png)',
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
    // Task instance folders have no Folder tab, so the inline path stays plain text (not a broken link).
    expect(screen.queryByRole('link', { name: `${instanceRoot}/shots/chart.svg` })).toBeNull()
    expect(screen.getByText(`${instanceRoot}/shots/chart.svg`)).toBeInTheDocument()
    expect(screen.getByText(`${baseAgentRoot}/shots/other.png`)).toBeInTheDocument()
    first.unmount()

    // Once the task ends and its instance folder is reclaimed (content 404), no thumbnails or error boxes appear.
    reclaimed = true
    const second = renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
    expect(await screen.findByText(`${instanceRoot}/shots/chart.svg`)).toBeInTheDocument()
    await waitFor(() => expect(requested.length).toBeGreaterThanOrEqual(4))
    expect(second.container.querySelector('[data-md-image]')).toBeNull()
    expect(second.container.querySelector('[data-md-image-error]')).toBeNull()
    expect(second.container.querySelector('img')).toBeNull()
  })
})
