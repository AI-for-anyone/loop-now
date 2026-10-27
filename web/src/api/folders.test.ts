import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from './client'
import { folderSource } from './folders'
import { apiUrl, errorBody } from '@/test/api'
import { server } from '@/test/server'

const entry = (path: string, type: Schemas['FolderEntry']['type'] = 'file'): Schemas['FolderEntry'] => ({ name: path.split('/').at(-1)!, path, type, size: type === 'file' ? 5 : null, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: null })

describe('文件夹接口', () => {
  it.each([
    ['agent', { kind: 'agent', agentId: 'a1' }, '/agents/a1/folder'],
    ['project', { kind: 'project', projectId: 'p1' }, '/projects/p1/context-folder'],
    ['global', { kind: 'global' }, '/context-folder'],
  ] as const)('%s：一层的内容按 path、cursor、limit 读取', async (_, scope, prefix) => {
    let query: URLSearchParams | null = null
    server.use(http.get(apiUrl(`${prefix}/entries`), ({ request }) => {
      query = new URL(request.url).searchParams
      return HttpResponse.json({ path: 'docs', entries: [entry('docs/a.md')], nextCursor: 'next' })
    }))
    const page = await folderSource(scope).list('docs', 'c1')
    expect(page).toEqual({ path: 'docs', entries: [entry('docs/a.md')], nextCursor: 'next' })
    expect(Object.fromEntries(query!)).toEqual({ path: 'docs', limit: '500', cursor: 'c1' })
  })

  it('读取原始内容；保存以原始字节 PUT，create 表示只新建', async () => {
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
    expect(await source.write('AGENTS.md', '# 说明\n', true)).toEqual(entry('AGENTS.md'))
    await source.write('AGENTS.md', '# 改过\n', false)
    expect(puts).toEqual([
      { query: { path: 'AGENTS.md', create: 'true' }, type: 'application/octet-stream', body: '# 说明\n' },
      { query: { path: 'AGENTS.md', create: 'false' }, type: 'application/octet-stream', body: '# 改过\n' },
    ])
  })

  it('已存在时 409 path_conflict；新建文件夹、移动与删除的请求', async () => {
    const calls: string[] = []
    server.use(
      http.put(apiUrl('/context-folder/content'), () => HttpResponse.json(errorBody('path_conflict', '已存在'), { status: 409 })),
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

  it('上传逐个文件 PUT（覆盖同名），失败时与其他请求一样给出错误码', async () => {
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

  it('Agent 的文件夹：从 Git 拉取带新的 actionId；最近的拉取记录新的在前', async () => {
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
})
