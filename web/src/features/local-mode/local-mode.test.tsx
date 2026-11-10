import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { FileManager } from '@/features/folders/FileManager'
import type { FolderSource, GitPull } from '@/features/folders/folder-source'
import { apiUrl, defaultProject, errorBody, makeAgent, makeProject } from '@/test/api'
import { renderConfig } from '@/test/config'
import { createMockFolder } from '@/test/folders'
import { dir, localModeBackend } from '@/test/local-mode'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const limits = { maxBytes: 1 << 20, extensions: ['.md'] }
const region = (name: string) => screen.findByRole('region', { name })
const cautions = ['改动直接落在真实文件上：Agent 在这些目录里做的修改直接写进你的文件，任务里也一样，不走任务副本。', '这份清单是告诉 Agent 去哪里干活，不是访问隔离：不在清单里的目录，Agent 同样能读写。']

describe('全局设置：本机模式', () => {
  it('四个开关各有说明，按下即保存那一项；失败时开关保持原状并说明原因', async () => {
    const local = localModeBackend({ settings: { git: false } })
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const card = await region('本机模式')
    const directories = await within(card).findByRole('switch', { name: '绑定本机目录' })
    const git = within(card).getByRole('switch', { name: '用本机的 git 身份' })
    const cli = within(card).getByRole('switch', { name: '用本机的 CLI' })
    const logins = within(card).getByRole('switch', { name: '用本机的登录状态' })
    expect(within(card).getAllByRole('switch')).toEqual([directories, git, cli, logins])
    expect(within(card).getByText('让 Agent 直接用这台电脑上已有的东西。四项各自开关：前三项默认开启，“用本机的登录状态”默认关闭，要你自己打开。')).toBeInTheDocument()
    expect(within(card).getByText(/“绑定本机目录”“用本机的 CLI”和“用本机的登录状态”在 Agent 处理下一条消息时生效/)).toBeInTheDocument()
    expect(directories).toBeChecked()
    expect(git).not.toBeChecked()
    expect(cli).toBeChecked()
    expect(git).toHaveAccessibleDescription(/HTTPS 地址可以用这台电脑上已保存的 git 凭证.*SSH 地址用 ~\/\.ssh 下的密钥不受这个开关影响/)
    expect(cli).toHaveAccessibleDescription(/登录状态仍用 Loop Now 里配置的账号.*要沿用，另外打开“用本机的登录状态”/)
    expect(logins).not.toBeChecked()
    expect(logins).toHaveAccessibleDescription(/等同以你本人的身份操作，所有 Agent 共用这些登录；Loop Now 里已为 Agent 配了账号的 CLI 仍用配置的账号/)
    expect(directories).toHaveAccessibleDescription(/关闭后清单保留，但不再告诉任何 Agent/)

    await user.click(git)
    await waitFor(() => expect(git).toBeChecked())
    expect(local.state.requests).toEqual([{ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { git: true } }])

    local.state.failNextPut = () => HttpResponse.json(errorBody('internal_error', '写入失败'), { status: 500 })
    await user.click(cli)
    expect(await within(card).findByRole('alert')).toBeInTheDocument()
    expect(cli).toBeChecked()
    expect(local.state.requests.at(-1)).toEqual({ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { cli: false } })
  })

  it('打开“用本机的登录状态”先确认，写明以你本人的身份操作、所有 Agent 共用、已配账号的 CLI 不变；取消则不保存', async () => {
    const local = localModeBackend()
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const logins = await within(await region('本机模式')).findByRole('switch', { name: '用本机的登录状态' })

    await user.click(logins)
    let dialog = await screen.findByRole('alertdialog', { name: '打开“用本机的登录状态”？' })
    expect(dialog).toHaveTextContent('Agent 直接用你在这台电脑上各个工具的登录，等同以你本人的身份操作。')
    expect(dialog).toHaveTextContent('所有 Agent 共用这些登录，任务里的 Agent 也一样。')
    expect(dialog).toHaveTextContent('Loop Now 里已为 Agent 配了账号的 CLI 仍用配置的账号。')
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(logins).not.toBeChecked()
    expect(local.state.requests).toEqual([])

    await user.click(logins)
    dialog = await screen.findByRole('alertdialog', { name: '打开“用本机的登录状态”？' })
    await user.click(within(dialog).getByRole('button', { name: '打开' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    await waitFor(() => expect(logins).toBeChecked())
    expect(local.state.requests).toEqual([{ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { logins: true } }])

    // 关闭不用确认。
    await user.click(logins)
    await waitFor(() => expect(logins).not.toBeChecked())
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(local.state.requests.at(-1)).toEqual({ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { logins: false } })
  })

  it('确认后保存失败：错误留在确认框里，开关保持关闭', async () => {
    const local = localModeBackend()
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const card = await region('本机模式')
    const logins = await within(card).findByRole('switch', { name: '用本机的登录状态' })
    local.state.failNextPut = () => HttpResponse.json(errorBody('internal_error', '写入失败'), { status: 500 })
    await user.click(logins)
    const dialog = await screen.findByRole('alertdialog', { name: '打开“用本机的登录状态”？' })
    await user.click(within(dialog).getByRole('button', { name: '打开' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument()
    expect(logins).not.toBeChecked()
    // 卡片上不再重复显示同一个错误；关掉确认框后也不留。
    expect(within(card).queryByRole('alert', { hidden: true })).toBeNull()
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(within(card).queryByRole('alert')).toBeNull()
  })
})

describe('项目设置：本机目录', () => {
  const project = makeProject({ name: '模型接入' })
  const path = `/p/${encodeURIComponent(project.name)}/settings`
  function setup(init: Parameters<typeof localModeBackend>[0] = {}) {
    const local = localModeBackend({ projects: { [project.id]: { revision: 2, directories: [dir('/Users/me/code/app', '主仓库'), dir('/Users/me/missing-dir')] } }, ...init })
    server.use(...local.handlers, ...projectBackend({ projects: [defaultProject, project] }).handlers)
    return { local, user: userEvent.setup(), ...renderRoutes(routes, path) }
  }
  const rows = async () => within(await screen.findByRole('list', { name: '本机目录列表' })).getAllByRole('listitem')

  it('列出路径与备注，目录不存在的那一行标出来；如实说明改动落在真实文件上、清单不是访问隔离', async () => {
    setup()
    const card = await region('本机目录')
    const [app, missing] = await rows()
    expect(app).toHaveTextContent('/Users/me/code/app')
    expect(app).toHaveTextContent('主仓库')
    expect(within(app).queryByText('目录不存在')).toBeNull()
    expect(within(missing).getByText('目录不存在')).toBeInTheDocument()
    expect(within(missing).getByText('这一项暂不告诉 Agent。')).toBeInTheDocument()
    for (const caution of cautions) expect(within(card).getByText(caution)).toBeInTheDocument()
    // 开关开着时不提示“清单暂不生效”。
    expect(within(card).queryByText(/已关闭/)).toBeNull()
  })

  it('添加：整份清单带修订号保存，新目录不存在也照常保存并在行内标出', async () => {
    const { local, user } = setup()
    await user.click(await within(await region('本机目录')).findByRole('button', { name: '添加目录' }))
    const dialog = await screen.findByRole('dialog', { name: '添加本机目录' })
    await user.type(within(dialog).getByLabelText('绝对路径'), '  /Users/me/missing-new  ')
    await user.type(within(dialog).getByLabelText('备注（可选）'), '   ')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests).toEqual([{
      method: 'PUT',
      path: `/api/v1/projects/${project.id}/local-directories`,
      body: { expectedRevision: 2, directories: [{ path: '/Users/me/code/app', note: '主仓库' }, { path: '/Users/me/missing-dir', note: null }, { path: '/Users/me/missing-new', note: null }] },
    }])
    const added = (await rows())[2]
    expect(added).toHaveTextContent('/Users/me/missing-new')
    expect(within(added).getByText('目录不存在')).toBeInTheDocument()
  })

  it('编辑与移除：只改那一行；移除前说明不会删除文件', async () => {
    const { local, user } = setup()
    await user.click(await screen.findByRole('button', { name: '编辑 /Users/me/code/app' }))
    const dialog = await screen.findByRole('dialog', { name: '编辑本机目录' })
    const save = within(dialog).getByRole('button', { name: '保存' })
    expect(save).toBeDisabled()
    const note = within(dialog).getByLabelText('备注（可选）')
    await user.clear(note)
    await user.type(note, '前端')
    await user.click(save)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 2, directories: [{ path: '/Users/me/code/app', note: '前端' }, { path: '/Users/me/missing-dir', note: null }] })
    expect((await rows())[0]).toHaveTextContent('前端')

    await user.click(screen.getByRole('button', { name: '移除 /Users/me/missing-dir' }))
    const confirm = await screen.findByRole('alertdialog', { name: '从清单移除“/Users/me/missing-dir”？' })
    expect(confirm).toHaveTextContent('只从清单里移除，不会删除这个目录或其中的文件。')
    await user.click(within(confirm).getByRole('button', { name: '移除' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 3, directories: [{ path: '/Users/me/code/app', note: '前端' }] })
    expect(await rows()).toHaveLength(1)
  })

  it('服务端的字段错误显示在改动那一行的输入框下，对话框保留输入', async () => {
    const { local, user } = setup()
    local.state.failNextPut = () =>
      HttpResponse.json(errorBody('validation_failed', '提交的内容未通过校验', { fields: [{ field: 'directories[2].path', message: '必须是绝对路径' }] }), { status: 422 })
    await user.click(await screen.findByRole('button', { name: '添加目录' }))
    const dialog = await screen.findByRole('dialog', { name: '添加本机目录' })
    const input = within(dialog).getByLabelText('绝对路径')
    await user.type(input, 'code/app')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByText('必须是绝对路径')).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveValue('code/app')
    // 字段错误已在输入框下显示，不再整体重复。
    expect(within(dialog).queryByRole('alert')).toBeNull()
  })

  it('清单在别处改过（412）时读取最新内容并说明；再保存时套在新清单上', async () => {
    const { local, user } = setup()
    await user.click(await screen.findByRole('button', { name: '添加目录' }))
    const dialog = await screen.findByRole('dialog', { name: '添加本机目录' })
    await user.type(within(dialog).getByLabelText('绝对路径'), '/srv/data')
    local.state.projects.set(project.id, { revision: 3, directories: [dir('/Users/me/other')] })
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByText('清单刚在别处被修改，已读取最新内容；请核对后再保存一次。')).toBeInTheDocument()
    // 对话框打开时页面其余部分对读屏隐藏，所以带 hidden 查询背后已经换成最新的清单。
    await waitFor(() => expect(within(screen.getByRole('list', { name: '本机目录列表', hidden: true })).getAllByRole('listitem', { hidden: true }).map((row) => row.getAttribute('aria-label'))).toEqual(['/Users/me/other']))
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 3, directories: [{ path: '/Users/me/other', note: null }, { path: '/srv/data', note: null }] })
  })

  it('“绑定本机目录”关着时说明清单暂不交给 Agent，并链到全局设置', async () => {
    const { user, router } = setup({ settings: { directories: false } })
    const card = await region('本机目录')
    const note = await within(card).findByText(/全局设置里的“绑定本机目录”已关闭：清单照常保存，但不会告诉任何 Agent。/)
    expect(await rows()).toHaveLength(2)
    await user.click(within(note).getByRole('link', { name: '去全局设置' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
    expect(router.state.location.hash).toBe('#local-mode')
  })
})

describe('Agent 配置：本机目录', () => {
  it('继承自项目的条目只读并标明来源；Agent 自己的可以编辑，保存只替换自己的', async () => {
    const agent = makeAgent()
    const local = localModeBackend({
      projects: { [defaultProject.id]: { revision: 1, directories: [dir('/Users/me/shared', '项目资料')] } },
      agents: { [agent.id]: { revision: 4, directories: [dir('/Users/me/own')], projectId: defaultProject.id } },
    })
    const { user } = await renderConfig({ agent, handlers: local.handlers })
    const section = await region('本机目录')
    const inherited = await within(section).findByRole('region', { name: '来自项目“测试项目”' })
    expect(inherited).toHaveTextContent('/Users/me/shared')
    expect(inherited).toHaveTextContent('项目资料')
    expect(within(inherited).queryByRole('button')).toBeNull()
    expect(within(inherited).getByRole('link', { name: '在项目设置中修改' })).toHaveAttribute('href', `/p/${encodeURIComponent('测试项目')}/settings`)
    for (const caution of cautions) expect(within(section).getByText(caution)).toBeInTheDocument()

    const own = within(section).getByRole('list', { name: '本机目录列表' })
    expect(within(own).getAllByRole('listitem').map((row) => row.getAttribute('aria-label'))).toEqual(['/Users/me/own'])
    await user.click(within(section).getByRole('button', { name: '添加目录' }))
    const dialog = await screen.findByRole('dialog', { name: '添加本机目录' })
    await user.type(within(dialog).getByLabelText('绝对路径'), '/Users/me/notes')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests).toEqual([{ method: 'PUT', path: `/api/v1/agents/${agent.id}/local-directories`, body: { expectedRevision: 4, directories: [{ path: '/Users/me/own', note: null }, { path: '/Users/me/notes', note: null }] } }])
    expect(within(inherited).getAllByRole('listitem')).toHaveLength(1)
  })

  it('平台 Agent 没有继承的条目', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null, name: '平台助手' })
    const local = localModeBackend({ agents: { [agent.id]: { revision: 0, directories: [], projectId: null } } })
    await renderConfig({ agent, handlers: local.handlers })
    const section = await region('本机目录')
    expect(await within(section).findByText('还没有登记本机目录。')).toBeInTheDocument()
    expect(within(section).getByText('告诉这个 Agent 可以去这些本机目录干活。')).toBeInTheDocument()
    expect(within(section).queryByRole('region')).toBeNull()
  })
})

describe('从 Git 拉取失败：本机 git 身份的提示', () => {
  const HTTPS = 'https://code.example.net/team/app.git'
  const SSH = 'git@code.example.net:team/app.git'
  const failedPull = (errorCode: GitPull['errorCode'], url: string): GitPull => ({
    id: crypto.randomUUID(), agentId: 'a', url, branch: 'main', path: 'app', status: 'failed', errorCode,
    errorMessage: 'fatal: Authentication failed', createdAt: '2026-11-10T08:00:00Z', finishedAt: '2026-11-10T08:00:05Z',
  })
  // 开关的读取次数：只有 HTTPS 地址认证失败时才需要读。
  function setup(errorCode: GitPull['errorCode'], url: string, git: boolean) {
    const reads = { count: 0 }
    server.use(http.get(apiUrl('/local-mode-settings'), () => {
      reads.count++
      return HttpResponse.json({ directories: true, git, cli: true, logins: false } satisfies Schemas['LocalModeSettings'])
    }))
    const base = createMockFolder({ key: 'w', preset: 'workspace', latency: 0, git: true })
    const source: FolderSource = { ...base, git: { ...base.git!, list: async () => [failedPull(errorCode, url)] } }
    const view = renderRoutes([
      { path: '/', element: <FileManager source={source} rootLabel="开发助手 的文件夹" limits={limits} className="h-[600px]" /> },
      { path: '/settings', element: <p>全局设置</p> },
    ], '/')
    return { reads, ...view }
  }

  it('HTTPS 地址认证失败且开关关着：提示去打开，链到全局设置的本机模式', async () => {
    const { router } = setup('auth_failed', HTTPS, false)
    expect(await screen.findByText('拉取失败：认证失败')).toBeInTheDocument()
    const hint = await screen.findByRole('note')
    expect(hint).toHaveTextContent('“用本机的 git 身份”没有打开，HTTPS 地址的拉取用不到这台电脑上已保存的 git 凭证。')
    await userEvent.setup().click(within(hint).getByRole('link', { name: '去全局设置打开' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
    expect(router.state.location.hash).toBe('#local-mode')
  })

  it('HTTPS 地址认证失败但开关开着：不额外提示，照常可以看 git 输出', async () => {
    const { reads } = setup('auth_failed', HTTPS, true)
    expect(await screen.findByText('拉取失败：认证失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '查看 git 输出' })).toBeInTheDocument()
    await waitFor(() => expect(reads.count).toBe(1))
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('SSH 地址认证失败：开关关着也不提示去打开（~/.ssh 的密钥不受开关影响），照常看 git 输出', async () => {
    const { reads } = setup('auth_failed', SSH, false)
    expect(await screen.findByText('拉取失败：认证失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '查看 git 输出' })).toBeInTheDocument()
    expect(screen.queryByRole('note')).toBeNull()
    expect(reads.count).toBe(0)
  })

  it('SSH 主机未确认：不论开关，都提示先在本机终端核对指纹', async () => {
    const { reads } = setup('host_key_unverified', SSH, false)
    expect(await screen.findByText('拉取失败：SSH 主机密钥未确认')).toBeInTheDocument()
    const hint = await screen.findByRole('note')
    expect(hint).toHaveTextContent('ssh -T git@主机名')
    expect(within(hint).queryByRole('link')).toBeNull()
    expect(reads.count).toBe(0)
  })

  it('其他失败不读取开关、不提示', async () => {
    const { reads } = setup('clone_failed', HTTPS, false)
    expect(await screen.findByText('拉取失败：无法克隆仓库')).toBeInTheDocument()
    expect(screen.queryByRole('note')).toBeNull()
    expect(reads.count).toBe(0)
  })
})
