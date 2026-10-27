import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, context, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'

const summary = (id: string, name: string, description: string): Schemas['SkillTemplateSummary'] => ({
  id,
  name,
  description,
  fileCount: 2,
  createdAt: '2026-10-12T00:00:00Z',
  updatedAt: '2026-10-12T00:00:00Z',
})

const detail = (overrides: Partial<Schemas['SkillTemplate']> = {}): Schemas['SkillTemplate'] => ({
  id: 'sk-1',
  name: 'pdf-tools',
  description: '处理 PDF',
  files: [
    { path: 'SKILL.md', size: 40, sha256: 'a' },
    { path: 'scripts/run.sh', size: 7, sha256: 'b' },
  ],
  skillMd: '---\nname: pdf-tools\ndescription: 处理 PDF\n---\n',
  createdAt: '2026-10-12T00:00:00Z',
  updatedAt: '2026-10-12T00:00:00Z',
  ...overrides,
})

function setup(path: string, handlers: HttpHandler[] = [], skills: Schemas['SkillTemplateSummary'][] = []) {
  server.use(
    ...handlers,
    http.get(apiUrl('/context'), () => HttpResponse.json(context)),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills })),
    http.get(apiUrl('/skills/sk-1'), () => HttpResponse.json(detail())),
  )
  const user = userEvent.setup()
  return { user, ...renderRoutes(routes, path) }
}

function withPath(content: string, relativePath: string) {
  const file = new File([content], relativePath.split('/').at(-1)!)
  Object.defineProperty(file, 'webkitRelativePath', { value: relativePath })
  return file
}

describe('模板列表', () => {
  it('列出模板的名称、描述与更新时间', async () => {
    setup('/skills', [], [summary('a', 'pdf-tools', '处理 PDF'), summary('b', 'git-helper', '协助 git 操作')])
    const list = await screen.findByRole('list', { name: 'Skills 库' })
    await waitFor(() => expect(within(list).getAllByRole('link')).toHaveLength(2))
    expect(within(list).getByText('协助 git 操作')).toBeInTheDocument()
    expect(within(list).getAllByText(/更新于/)).toHaveLength(2)
  })

  it('库为空时显示空状态与上传、新建入口', async () => {
    setup('/skills')
    expect(await screen.findByText('Skills 库中还没有模板。')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上传目录' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上传 zip' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '在线新建' })).toBeInTheDocument()
  })
})

describe('上传 Skill', () => {
  it('上传目录：打包为 zip 后以 multipart 上传，成功后打开模板', async () => {
    let contentType: string | null = null
    let hasArchive = false
    const { user, router } = setup('/skills', [
      http.post(apiUrl('/skills'), async ({ request }) => {
        contentType = request.headers.get('Content-Type')
        // jsdom 环境中 multipart 的文件内容不可靠（见 test/setup.ts），这里只检查字段名。
        hasArchive = (await request.text()).includes('name="archive"')
        return HttpResponse.json(detail(), { status: 201 })
      }),
    ])
    await user.upload(await screen.findByLabelText('上传目录'), [withPath('---\nname: pdf-tools\n---', 'pdf-tools/SKILL.md'), withPath('echo hi', 'pdf-tools/scripts/run.sh')])
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills/sk-1'))
    expect(contentType).toMatch(/^multipart\/form-data/)
    expect(hasArchive).toBe(true)
  })

  it('缺少 SKILL.md 时显示原因，没有新增模板', async () => {
    const { user, router } = setup('/skills', [
      http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('validation_failed', '缺少 SKILL.md'), { status: 422 })),
    ])
    await user.upload(await screen.findByLabelText('上传目录'), [withPath('echo hi', 'x/run.sh')])
    expect(await screen.findByText(/缺少 SKILL.md/)).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/skills')
  })

  it('名称重复时提示改用替换', async () => {
    const { user } = setup('/skills', [http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))])
    await user.upload(await screen.findByLabelText('上传 zip'), new File(['zip'], 'pdf-tools.zip', { type: 'application/zip' }))
    expect(await screen.findByText(/请打开该模板并使用“替换”/)).toBeInTheDocument()
  })
})

describe('在线新建 Skill', () => {
  it('编辑器预填骨架，保存后创建模板', async () => {
    let body: unknown = null
    const { user, router } = setup('/skills/new', [
      http.post(apiUrl('/skills'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail(), { status: 201 })
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: '新建 Skill' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    expect((editor as HTMLTextAreaElement).value).toMatch(/^---\nname: .+\ndescription: .+\n---/)
    await user.click(within(sheet).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(body).toEqual({ skillMd: (editor as HTMLTextAreaElement).value }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills/sk-1'))
  })

  it('缺少描述时显示原因并保留内容', async () => {
    const { user } = setup('/skills/new', [
      http.post(apiUrl('/skills'), () => HttpResponse.json(errorBody('validation_failed', 'frontmatter 缺少 description'), { status: 422 })),
    ])
    const sheet = await screen.findByRole('dialog', { name: '新建 Skill' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.clear(editor)
    await user.type(editor, '---{Enter}name: x{Enter}---')
    await user.click(within(sheet).getByRole('button', { name: '创建' }))
    expect(await within(sheet).findByText(/缺少 description/)).toBeInTheDocument()
    expect(editor).toHaveValue('---\nname: x\n---')
  })
})

describe('查看与编辑模板的 SKILL.md', () => {
  it('显示文件列表；保存只提交 SKILL.md', async () => {
    let body: unknown = null
    const { user } = setup('/skills/sk-1', [
      http.put(apiUrl('/skills/sk-1/skill-md'), async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(detail())
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    const files = await within(sheet).findByRole('list', { name: '文件' })
    expect(within(files).getByText('scripts/run.sh')).toBeInTheDocument()
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.type(editor, '正文')
    await user.click(within(sheet).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(body).toEqual({ content: '---\nname: pdf-tools\ndescription: 处理 PDF\n---\n正文' }))
  })

  it('后台刷新失败时保留未保存的编辑，并显示原因', async () => {
    let fail = false
    const { user, client } = setup('/skills/sk-1', [
      http.get(apiUrl('/skills/sk-1'), () => (fail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(detail()))),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    const editor = within(sheet).getByLabelText('SKILL.md')
    await user.type(editor, '还没保存')
    fail = true
    await act(() => client.refetchQueries({ queryKey: keys.skill('sk-1') }))
    expect(await within(sheet).findByText('服务端内部错误')).toBeInTheDocument()
    expect(within(sheet).getByLabelText('SKILL.md')).toHaveValue('---\nname: pdf-tools\ndescription: 处理 PDF\n---\n还没保存')
  })
})

describe('替换与删除模板', () => {
  it('替换前确认，说明副本不受影响', async () => {
    let replaced = false
    const { user } = setup('/skills/sk-1', [
      http.put(apiUrl('/skills/sk-1'), () => {
        replaced = true
        return HttpResponse.json(detail())
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    await within(sheet).findByRole('list', { name: '文件' })
    // 网格的工具栏也有“上传目录”（用于新建），替换只用抽屉里的那个。
    await user.upload(within(sheet).getByLabelText('上传目录'), [withPath('---\nname: pdf-tools\n---', 'pdf-tools/SKILL.md')])
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('已复制给 Agent 的 Skill 副本不受影响')
    expect(replaced).toBe(false)
    await user.click(within(confirm).getByRole('button', { name: '替换' }))
    await waitFor(() => expect(replaced).toBe(true))
  })

  it('删除前确认，删除后回到列表', async () => {
    let deleted = false
    const { user, router } = setup('/skills/sk-1', [
      http.delete(apiUrl('/skills/sk-1'), () => {
        deleted = true
        return new HttpResponse(null, { status: 204 })
      }),
    ])
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    await within(sheet).findByRole('list', { name: '文件' })
    await user.click(within(sheet).getByRole('button', { name: '删除' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('已复制给 Agent 的 Skill 副本不受影响')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
  })
})

describe('抽屉由地址控制开合', () => {
  const templates = [summary('sk-1', 'pdf-tools', '处理 PDF'), summary('b', 'git-helper', '协助 git 操作')]

  it('直接打开 /skills/:skillId 时显示网格并打开详情抽屉，关闭后回到 /skills', async () => {
    const { user, router } = setup('/skills/sk-1', [], templates)
    const sheet = await screen.findByRole('dialog', { name: 'pdf-tools' })
    expect(within(sheet).getByText('处理 PDF')).toBeInTheDocument()
    // 抽屉是模态的，网格对辅助技术暂时隐藏，但仍在页面上。
    const list = await screen.findByRole('list', { name: 'Skills 库', hidden: true })
    await waitFor(() => expect(within(list).getAllByRole('link', { hidden: true })).toHaveLength(2))
    expect(within(list).getByRole('link', { name: /pdf-tools/, hidden: true })).toHaveAttribute('aria-current', 'true')

    await user.click(within(sheet).getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('list', { name: 'Skills 库' })).toBeInTheDocument()
  })

  it('直接打开 /skills/new 时显示网格并打开在线新建抽屉，按 Esc 关闭后回到 /skills', async () => {
    const { user, router } = setup('/skills/new', [], templates)
    const sheet = await screen.findByRole('dialog', { name: '新建 Skill' })
    expect(within(sheet).getByLabelText('SKILL.md')).toBeInTheDocument()
    expect(await screen.findByRole('list', { name: 'Skills 库', hidden: true })).toBeInTheDocument()

    await user.keyboard('{Escape}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('在线新建抽屉的取消按钮关闭抽屉，不创建模板', async () => {
    const { user, router } = setup('/skills/new', [], templates)
    const sheet = await screen.findByRole('dialog', { name: '新建 Skill' })
    await user.click(within(sheet).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/skills'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('点击模板卡片打开详情抽屉', async () => {
    const { user, router } = setup('/skills', [], templates)
    const list = await screen.findByRole('list', { name: 'Skills 库' })
    await user.click(await within(list).findByRole('link', { name: /pdf-tools/ }))
    expect(await screen.findByRole('dialog', { name: 'pdf-tools' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/skills/sk-1')
  })

  it('模板不存在时抽屉内说明原因', async () => {
    setup('/skills/gone', [http.get(apiUrl('/skills/gone'), () => HttpResponse.json(errorBody('not_found'), { status: 404 }))], templates)
    const sheet = await screen.findByRole('dialog', { name: '模板详情' })
    expect(await within(sheet).findByText('这个模板不存在或已删除。')).toBeInTheDocument()
  })
})
