import { waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from './client'
import { agentFolderContentURL, agentFolderRawURL, checkAgentFolderDirectory, folderRoots, folderSource, taskNodeFolderContentURL, taskNodeFolderRawURL, useAgentFolderLocation, useTaskNodeFolderLocation } from './folders'
import { apiUrl, errorBody } from '@/test/api'
import { renderHookWithQuery } from '@/test/render'
import { server } from '@/test/server'

const entry = (path: string, type: Schemas['FolderEntry']['type'] = 'file'): Schemas['FolderEntry'] => ({ name: path.split('/').at(-1)!, path, type, size: type === 'file' ? 5 : null, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: null })

describe('folder API', () => {
  it.each([
    ['agent', { kind: 'agent', agentId: 'a1' }, '/agents/a1/folder'],
    ['project', { kind: 'project', projectId: 'p1' }, '/projects/p1/context-folder'],
    ['global', { kind: 'global' }, '/context-folder'],
  ] as const)('%s: reads the entries of one level by path, cursor and limit', async (_, scope, prefix) => {
    let query: URLSearchParams | null = null
    server.use(http.get(apiUrl(`${prefix}/entries`), ({ request }) => {
      query = new URL(request.url).searchParams
      return HttpResponse.json({ path: 'docs', entries: [entry('docs/a.md')], nextCursor: 'next' })
    }))
    const page = await folderSource(scope).list('docs', 'c1')
    expect(page).toEqual({ path: 'docs', entries: [entry('docs/a.md')], nextCursor: 'next' })
    expect(Object.fromEntries(query!)).toEqual({ path: 'docs', limit: '500', cursor: 'c1' })
  })

  it('reads the raw content; saves with a PUT of the raw bytes, where create means create only', async () => {
    const puts: { query: Record<string, string>; type: string | null; body: string }[] = []
    server.use(
      http.get(apiUrl('/agents/a1/folder/content'), () => new HttpResponse(new Uint8Array([0xef, 0xbb, 0xbf, 0x41]), { headers: { 'Content-Type': 'application/octet-stream' } })),
      http.put(apiUrl('/agents/a1/folder/content'), async ({ request }) => {
        puts.push({ query: Object.fromEntries(new URL(request.url).searchParams), type: request.headers.get('Content-Type'), body: await request.text() })
        return HttpResponse.json(entry('AGENTS.md'))
      }),
    )
    const source = folderSource({ kind: 'agent', agentId: 'a1' })
    expect(new Uint8Array(await (await source.read('AGENTS.md')).arrayBuffer())).toEqual(new Uint8Array([0xef, 0xbb, 0xbf, 0x41]))
    expect(await source.write('AGENTS.md', '# Notes\n', true)).toEqual(entry('AGENTS.md'))
    await source.write('AGENTS.md', '# Changed\n', false)
    expect(puts).toEqual([
      { query: { path: 'AGENTS.md', create: 'true' }, type: 'application/octet-stream', body: '# Notes\n' },
      { query: { path: 'AGENTS.md', create: 'false' }, type: 'application/octet-stream', body: '# Changed\n' },
    ])
  })

  it('409 path_conflict when it already exists; the requests for new folder, move and delete', async () => {
    const calls: string[] = []
    server.use(
      http.put(apiUrl('/context-folder/content'), () => HttpResponse.json(errorBody('path_conflict', 'Already exists'), { status: 409 })),
      http.post(apiUrl('/context-folder/directories'), async ({ request }) => { calls.push(`mkdir ${JSON.stringify(await request.json())}`); return HttpResponse.json(entry('docs', 'dir'), { status: 201 }) }),
      http.post(apiUrl('/context-folder/move'), async ({ request }) => { calls.push(`move ${JSON.stringify(await request.json())}`); return HttpResponse.json(entry('archive/a.md')) }),
      http.delete(apiUrl('/context-folder/entries'), ({ request }) => { calls.push(`delete ${new URL(request.url).searchParams.get('path')}`); return new HttpResponse(null, { status: 204 }) }),
    )
    const source = folderSource({ kind: 'global' })
    await expect(source.write('a.md', 'x', true)).rejects.toMatchObject({ status: 409, code: 'path_conflict' })
    await source.mkdir('docs')
    await source.move('a.md', 'archive/a.md')
    await source.remove('archive')
    expect(calls).toEqual(['mkdir {"path":"docs"}', 'move {"from":"a.md","to":"archive/a.md"}', 'delete archive'])
    expect(source.git).toBeUndefined()
  })

  it('upload PUTs file by file (overwriting the same name); a failure gives an error code like any other request', async () => {
    const puts: string[] = []
    server.use(http.put(apiUrl('/projects/p1/context-folder/content'), ({ request }) => {
      const query = new URL(request.url).searchParams
      puts.push(`${query.get('path')} create=${query.get('create')}`)
      return query.get('path')!.endsWith('big.bin') ? HttpResponse.json(errorBody('payload_too_large'), { status: 413 }) : HttpResponse.json(entry(query.get('path')!))
    }))
    const source = folderSource({ kind: 'project', projectId: 'p1' })
    await source.upload('docs', { path: 'shots/a.png', file: new File(['png'], 'a.png') }, () => {})
    await expect(source.upload('docs', { path: 'big.bin', file: new File(['x'], 'big.bin') }, () => {})).rejects.toMatchObject({ status: 413, code: 'payload_too_large' })
    expect(puts).toEqual(['docs/shots/a.png create=false', 'docs/big.bin create=false'])
    expect(source.contentURL('docs/a b.md')).toMatch(/\/projects\/p1\/context-folder\/content\?path=docs%2Fa\+b\.md$/)
  })

  it('Agent folder: a pull from Git carries a new actionId; recent pulls come newest first', async () => {
    const bodies: Schemas['FolderGitPullInput'][] = []
    const pull: Schemas['FolderGitPull'] = { id: 'op1', agentId: 'a1', url: 'https://github.com/acme/x.git', branch: 'main', path: 'x', status: 'queued', errorCode: null, errorMessage: null, createdAt: '2026-10-25T08:00:00Z', finishedAt: null }
    let limit: string | null = null
    server.use(
      http.post(apiUrl('/agents/a1/folder/git-pulls'), async ({ request }) => { bodies.push(await request.json() as Schemas['FolderGitPullInput']); return HttpResponse.json(pull, { status: 202 }) }),
      http.get(apiUrl('/agents/a1/folder/git-pulls'), ({ request }) => { limit = new URL(request.url).searchParams.get('limit'); return HttpResponse.json({ pulls: [pull] }) }),
    )
    const git = folderSource({ kind: 'agent', agentId: 'a1' }).git!
    expect(await git.start({ url: pull.url, branch: 'main', path: 'x' })).toEqual(pull)
    expect(bodies).toEqual([{ actionId: expect.stringMatching(/^[\da-f-]{36}$/), url: pull.url, branch: 'main', path: 'x' }])
    expect(await git.list()).toEqual([pull])
    expect(limit).toBe('20')
  })

  it('reads folder locations for an Agent and a task node, builds their content URLs, and deduplicates roots', async () => {
    server.use(
      http.get(apiUrl('/agents/a1/folder'), () => HttpResponse.json({ path: '/var/work/a1/workspace', realPath: '/private/var/work/a1/workspace' })),
      http.get(apiUrl('/tasks/t1/nodes/n1/folder'), () => HttpResponse.json({ path: '/work/t1/workspace', realPath: '/work/t1/workspace' })),
    )
    const agent = renderHookWithQuery(() => useAgentFolderLocation('a1'))
    const node = renderHookWithQuery(() => useTaskNodeFolderLocation('t1', 'n1'))
    await waitFor(() => expect(agent.result.current.isSuccess).toBe(true))
    await waitFor(() => expect(node.result.current.isSuccess).toBe(true))
    expect(folderRoots(agent.result.current.data)).toEqual(['/var/work/a1/workspace', '/private/var/work/a1/workspace'])
    expect(folderRoots(node.result.current.data)).toEqual(['/work/t1/workspace'])
    expect(folderRoots(undefined)).toEqual([])
    expect(agentFolderContentURL('a1', 'assets/preview.png')).toMatch(/\/agents\/a1\/folder\/content\?path=assets%2Fpreview\.png$/)
    expect(taskNodeFolderContentURL('t1', 'n1', 'assets/preview.svg')).toMatch(/\/tasks\/t1\/nodes\/n1\/folder\/content\?path=assets%2Fpreview\.svg$/)
    expect(agentFolderRawURL('a1', 'my site/page 1.html')).toMatch(/\/agents\/a1\/folder\/raw\/my%20site\/page%201\.html$/)
    expect(taskNodeFolderRawURL('t1', 'n1', 'site/pelican.html')).toMatch(/\/tasks\/t1\/nodes\/n1\/folder\/raw\/site\/pelican\.html$/)
    expect(folderSource({ kind: 'agent', agentId: 'a1' }).rawURL?.('site/index.html')).toMatch(/\/agents\/a1\/folder\/raw\/site\/index\.html$/)
    expect(folderSource({ kind: 'project', projectId: 'p1' }).rawURL).toBeUndefined()
    expect(folderSource({ kind: 'global' }).rawURL).toBeUndefined()
  })

  it('checks whether an Agent folder path is a directory, a non-directory, or missing', async () => {
    server.use(
      http.get(apiUrl('/agents/a1/folder/entries'), ({ request }) => {
        const path = new URL(request.url).searchParams.get('path')
        if (path === 'assets') return HttpResponse.json({ path: 'assets', entries: [], nextCursor: null })
        if (path === 'missing') return new HttpResponse(null, { status: 404 })
        return new HttpResponse(null, { status: 422 })
      }),
    )
    expect(await checkAgentFolderDirectory('a1', 'assets')).toBe('dir')
    expect(await checkAgentFolderDirectory('a1', 'missing')).toBe('missing')
    expect(await checkAgentFolderDirectory('a1', 'escape.txt')).toBe('not-dir')
  })
})
