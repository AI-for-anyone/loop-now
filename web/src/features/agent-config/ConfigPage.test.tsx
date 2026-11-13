import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, defaultAgentsPath, errorBody, makeAgent, makeRun, toListItem } from '@/test/api'
import { credentialMeta, larkTemplate, renderConfig } from '@/test/config'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { modelOptions } from '@/test/model-picker'

const region = (name: string) => screen.getByRole('region', { name })

describe('版面', () => {
  it('“当前回复使用修改前的配置”与生效说明在同一张便签里', async () => {
    await renderConfig({
      agent: makeAgent({ configVersion: 3 }),
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 2 }) },
    })
    const stale = await screen.findByText('当前回复使用修改前的配置。')
    expect(stale.closest('[role="note"]')).toHaveTextContent('修改从下一条派发的消息开始生效，进行中的回复不受影响。')
  })

  it('复制内部 ID，成功后显示已复制', async () => {
    const { agent, user } = await renderConfig()
    const basic = region('基本信息')
    await user.click(within(basic).getByRole('button', { name: '复制' }))
    await expect(navigator.clipboard.readText()).resolves.toBe(agent.id)
    expect(within(basic).getByRole('button', { name: '已复制' })).toBeInTheDocument()
  })

  // jsdom 不排版，只能确认滚动区是定位容器；整页不被撑高由浏览器检查。
  it('页面的滚动区是定位容器，区块里的读屏说明不会撑高整页', async () => {
    await renderConfig()
    expect(region('基本信息').closest('.overflow-y-auto')).toHaveClass('relative')
  })
})

// 改版“一张印出来的纸”第 2 步：编号分区、黄色便签、票根里的目录尺。
describe('改版的版式', () => {
  const sections = ['基本信息', 'Prompt', '本机目录', '模型账号凭证', 'Skills', '可用 CLI', '插件权限', '删除 Agent']

  it('八个区块按顺序编号 01–08：编号是空心的装饰，标题加一笔黄色记号笔；区块之间是虚线，第一个上面没有', async () => {
    await renderConfig()
    sections.forEach((name, index) => {
      const section = region(name)
      const number = section.querySelector('[data-section-number]')!
      expect(number).toHaveTextContent(String(index + 1).padStart(2, '0'))
      expect(number).toHaveAttribute('aria-hidden', 'true')
      expect(number).toHaveClass('max-nav:hidden', 'text-transparent')
      expect(within(section).getByRole('heading', { level: 2, name })).toHaveClass('riso-marker', 'font-display-cn')
      if (index === 0) expect(section).not.toHaveClass('border-t-2')
      else expect(section).toHaveClass('border-t-2', 'border-dashed')
    })
  })

  it('区块不再是一张张卡片，直接排在纸上；删除 Agent 一区沿用红色', async () => {
    await renderConfig()
    expect(region('基本信息')).not.toHaveClass('shadow-print-rest')
    expect(region('基本信息')).not.toHaveClass('bg-paper-raised')
    const danger = region('删除 Agent')
    expect(within(danger).getByRole('heading', { level: 2, name: '删除 Agent' })).toHaveClass('text-red-text')
    expect(danger.querySelector('[data-section-number]')).toHaveClass('[-webkit-text-stroke:1.6px_var(--red-text)]')
    expect(danger.querySelector('[data-section-body]')).toHaveClass('riso-stripes-red', 'bg-error-field')
  })

  it('内部 ID 写在虚线框里，复制按钮是带藏青描边的黄色小方块', async () => {
    const { agent } = await renderConfig()
    const id = within(region('基本信息')).getByText(agent.id)
    expect(id.parentElement).toHaveClass('border-dashed', 'rounded-input')
    expect(within(region('基本信息')).getByRole('button', { name: '复制' })).toHaveClass('border-2', 'border-ink', 'bg-riso-yellow')
  })

  // jsdom 不排版：位置由浏览器检查确认（左栏“模型提供方”下面的说明不再把右栏“模型”往下推）。
  it('同一行的两栏字段按顶部对齐：一栏下面的说明往下长，不把旁边那栏往下推', async () => {
    await renderConfig()
    const basic = region('基本信息')
    // 内部 ID 与显示名称一行。
    expect(within(basic).getByLabelText('显示名称').closest('.grid')!.parentElement).toHaveClass('items-start')
    // 模型提供方与模型一行（模型选择器里的网格）。
    expect(within(basic).getByLabelText('模型提供方').closest('.grid')!.parentElement).toHaveClass('items-start')
  })

  it('正文一栏与目录都在纸张里，用纸张的细滚动条，不隐藏滚动条（目录尺不是滚动位置）', async () => {
    await renderConfig()
    const sheet = region('基本信息').closest('[data-sheet]')!
    expect(sheet).toHaveClass('riso-scrollbar')
    const body = region('基本信息').closest('.overflow-y-auto')!
    const toc = screen.getByRole('navigation', { name: '目录' })
    for (const area of [body, toc]) {
      expect(sheet).toContainElement(area as HTMLElement)
      expect(area.className).not.toMatch(/no-scrollbar/)
    }
  })

  it('“下一条消息生效”的提示是一张黄色便签', async () => {
    await renderConfig()
    const note = screen.getByText('修改从下一条派发的消息开始生效，进行中的回复不受影响。').closest('[role="note"]')!
    expect(note).toHaveClass('riso-tilt', '[--tilt-bg:var(--riso-yellow)]')
    expect(note.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })

  it('目录在纸张右侧的票根里：右缘一排刻度，每项是“名称 编号 短刻度”，当前一项用黄色记号笔加粉色箭头标出', async () => {
    await renderConfig()
    const toc = screen.getByRole('navigation', { name: '目录' })
    expect(toc).toHaveClass('border-l-2', 'border-dashed', 'max-nav:hidden')
    expect(within(toc).getByText('目录')).toHaveAttribute('aria-hidden', 'true')
    // 右缘一排刻度（装饰）。
    expect(toc.querySelector('ol > div[aria-hidden="true"]')).toHaveClass('absolute', 'right-0', 'w-3')
    expect(toc.querySelector('[data-ruler-mark]')).toBeNull()
    act(() => FakeIntersectionObserver.trigger(region('Skills'), true))
    const current = within(toc).getByRole('link', { name: 'Skills' })
    expect(current.closest('li')!.querySelector('[data-ruler-mark]')).not.toBeNull()
    expect(toc.querySelectorAll('[data-ruler-mark]')).toHaveLength(1)
    // 删除 Agent 一项是红色。
    expect(within(toc).getByRole('link', { name: '删除 Agent' })).toHaveClass('text-red-text')
  })
})

describe('分区目录', () => {
  const toc = () => screen.getByRole('navigation', { name: '目录' })
  const tocLink = (name: string) => within(toc()).getByRole('link', { name })
  const highlighted = () => within(toc()).getAllByRole('link').filter((link) => link.getAttribute('aria-current') === 'location')

  it('按顺序列出八个区块；点击目录项跳转到对应区块，焦点落在区块标题', async () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    try {
      const { user, router } = await renderConfig()
      expect(within(toc()).getAllByRole('link').map((link) => link.textContent)).toEqual([
        '01基本信息',
        '02Prompt',
        '03本机目录',
        '04模型账号凭证',
        '05Skills',
        '06可用 CLI',
        '07插件权限',
        '08删除 Agent',
      ])
      await user.click(tocLink('可用 CLI'))
      const clis = region('可用 CLI')
      expect(within(clis).getByRole('heading', { level: 2, name: '可用 CLI' })).toHaveFocus()
      expect(scroll.mock.contexts).toContain(clis)
      // 不改变地址。
      expect(router.state.location.pathname).toMatch(/\/config$/)
      expect(router.state.location.hash).toBe('')

      await user.click(tocLink('删除 Agent'))
      expect(within(region('删除 Agent')).getByRole('heading', { level: 2, name: '删除 Agent' })).toHaveFocus()
    } finally {
      scroll.mockRestore()
    }
  })

  it('高亮当前可见的区块，多个同时可见时取靠前的一个', async () => {
    await renderConfig()
    expect(highlighted()).toHaveLength(0)
    act(() => FakeIntersectionObserver.trigger(region('可用 CLI'), true))
    expect(highlighted()).toEqual([tocLink('可用 CLI')])
    act(() => FakeIntersectionObserver.trigger(region('Skills'), true))
    expect(highlighted()).toEqual([tocLink('Skills')])
    act(() => FakeIntersectionObserver.trigger(region('Skills'), false))
    expect(highlighted()).toEqual([tocLink('可用 CLI')])
    // 全部离开视口时保留上一次的高亮。
    act(() => FakeIntersectionObserver.trigger(region('可用 CLI'), false))
    expect(highlighted()).toEqual([tocLink('可用 CLI')])
  })

  it('浏览器不支持 IntersectionObserver 时不高亮，跳转仍然可用', async () => {
    vi.stubGlobal('IntersectionObserver', undefined)
    try {
      const { user } = await renderConfig()
      await user.click(tocLink('可用 CLI'))
      expect(within(region('可用 CLI')).getByRole('heading', { level: 2, name: '可用 CLI' })).toHaveFocus()
      expect(highlighted()).toHaveLength(0)
    } finally {
      vi.unstubAllGlobals()
    }
  })
})

describe('名称、模型与 Prompt', () => {
  it('显示只读的内部 ID；先选提供方，模型选项只列该提供方的模型', async () => {
    const { agent, user } = await renderConfig()
    const basic = region('基本信息')
    expect(within(basic).getByText(agent.id)).toBeInTheDocument()
    const provider = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(provider).toHaveValue('anthropic'))
    await user.selectOptions(provider, 'openai')
    await waitFor(() => expect(within(basic).getByRole('combobox', { name: '模型' })).toBeEnabled())
    expect(await modelOptions(user, basic)).toEqual(['GPT X（gpt-x）'])
  })

  it('当前模型不在目录中时标明', async () => {
    await renderConfig({ agent: makeAgent({ model: { provider: 'anthropic', modelId: 'retired-model' } }) })
    const basic = region('基本信息')
    expect(await within(basic).findByText('当前模型不在服务端的模型目录中')).toBeInTheDocument()
    expect(within(basic).getByText('anthropic / retired-model')).toBeInTheDocument()
  })

  it('离开前有未保存的 Prompt 时请人确认', async () => {
    const { user, router } = await renderConfig()
    await user.type(screen.getByLabelText('Agent Prompt'), '新的提示')
    await user.click(screen.getByRole('link', { name: '对话' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('有未保存的修改')
    expect(router.state.location.pathname).toMatch(/\/config$/)
    await user.click(within(dialog).getByRole('button', { name: '离开' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/\/chat$/))
  })

  it('改名重复时在名称旁显示原因', async () => {
    const { agent, user } = await renderConfig({
      handlers: [http.patch(apiUrl('/agents/:agentId'), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))],
    })
    const name = screen.getByLabelText('显示名称')
    await user.clear(name)
    await user.type(name, '重名')
    await user.click(within(region('基本信息')).getByRole('button', { name: '保存' }))
    expect(await screen.findByText('项目内已有同名的 Agent，请换一个名称')).toBeInTheDocument()
    expect(name).toHaveValue('重名')
    void agent
  })
})

describe('名称规则', () => {
  it('改名不符合命名规则时先在本地提示，不提交', async () => {
    let calls = 0
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.patch(apiUrl(`/agents/${agent.id}`), () => {
          calls += 1
          return HttpResponse.json(agent)
        }),
      ],
    })
    const name = screen.getByLabelText('显示名称')
    await user.clear(name)
    await user.type(name, '@开发')
    await user.click(within(region('基本信息')).getByRole('button', { name: '保存' }))
    expect(await screen.findByText('名称不能以“@”开头')).toBeInTheDocument()
    expect(name).toHaveValue('@开发')
    expect(calls).toBe(0)
  })
})

describe('配置生效说明', () => {
  it('常驻说明；回复进行中保存时提示不影响当前回复', async () => {
    let saved: unknown = null
    const agent = makeAgent()
    let current = agent
    const { user } = await renderConfig({
      agent,
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 1 }) },
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          saved = await request.json()
          current = { ...agent, name: '新的', configVersion: 2 }
          return HttpResponse.json(current)
        }),
      ],
    })
    expect(screen.getByText('修改从下一条派发的消息开始生效，进行中的回复不受影响。')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('显示名称'))
    await user.type(screen.getByLabelText('显示名称'), '新的')
    await user.click(within(region('基本信息')).getByRole('button', { name: '保存' }))
    expect(await screen.findByText('已保存。当前回复不受影响，修改从下一条消息开始生效。')).toBeInTheDocument()
    expect(saved).toEqual({ name: '新的' })
  })

  it('当前回复使用修改前的配置时另行提示', async () => {
    await renderConfig({
      agent: makeAgent({ configVersion: 3 }),
      status: { busy: true, currentRun: makeRun({ status: 'running', configVersion: 2 }) },
    })
    expect(await screen.findByText('当前回复使用修改前的配置。')).toBeInTheDocument()
  })
})

describe('Agent 的 Skills 副本', () => {
  const template: Schemas['SkillTemplateSummary'] = {
    id: 'tpl-1',
    name: 'pdf-tools',
    description: '处理 PDF',
    fileCount: 2,
    createdAt: '2026-10-12T00:00:00Z',
    updatedAt: '2026-10-12T00:00:00Z',
  }
  const copy = (overrides: Partial<Schemas['AgentSkillSummary']> = {}): Schemas['AgentSkillSummary'] => ({
    id: 'as-1',
    name: 'pdf-tools',
    description: '处理 PDF',
    sourceTemplateId: 'tpl-1',
    fileCount: 2,
    copiedAt: '2026-10-12T00:00:00Z',
    updatedAt: '2026-10-12T00:00:00Z',
    ...overrides,
  })

  it('从库中添加模板', async () => {
    let copied: Schemas['AgentSkillSummary'][] = []
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json({ skills: copied })),
        http.post(apiUrl(`/agents/${agent.id}/skills`), () => {
          copied = [copy()]
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' }, { status: 201 })
        }),
      ],
    })
    await user.click(within(region('Skills')).getByRole('button', { name: '从库中添加' }))
    await user.click(within(await screen.findByRole('listitem', { name: 'pdf-tools' })).getByRole('button', { name: '添加' }))
    const item = await within(region('Skills')).findByRole('listitem', { name: 'pdf-tools' })
    expect(item).toHaveTextContent('来源模板：pdf-tools')
  })

  it('添加同名 Skill 时提示先移除已有的', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      agentSkills: [copy()],
      handlers: [http.post(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json(errorBody('name_conflict', 'dup'), { status: 409 }))],
    })
    await user.click(within(region('Skills')).getByRole('button', { name: '从库中添加' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(within(dialog).getByRole('listitem', { name: 'pdf-tools' })).getByRole('button', { name: '添加' }))
    expect(await within(dialog).findByText('这个 Agent 已有同名的 Skill，请先移除已有的 Skill 再添加。')).toBeInTheDocument()
  })

  it('重新复制覆盖前确认，说明会覆盖改过的 SKILL.md', async () => {
    let recopied = 0
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      library: [template],
      agentSkills: [copy()],
      handlers: [
        http.post(apiUrl(`/agents/${agent.id}/skills/as-1/recopy`), () => {
          recopied += 1
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' })
        }),
      ],
    })
    await user.click(await within(region('Skills')).findByRole('button', { name: '用库中最新版重新复制' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('包括你对 SKILL.md 的修改')
    expect(recopied).toBe(0)
    await user.click(within(dialog).getByRole('button', { name: '用库中最新版重新复制' }))
    await waitFor(() => expect(recopied).toBe(1))
  })

  it('来源模板已删除时不可重新复制，编辑与移除仍可用', async () => {
    await renderConfig({ agentSkills: [copy({ sourceTemplateId: null })] })
    const item = await within(region('Skills')).findByRole('listitem', { name: 'pdf-tools' })
    expect(within(item).getByRole('button', { name: '用库中最新版重新复制' })).toBeDisabled()
    expect(within(item).getAllByText(/来源模板已删除/).length).toBeGreaterThan(0)
    expect(within(item).getByRole('button', { name: '编辑 SKILL.md' })).toBeEnabled()
    expect(within(item).getByRole('button', { name: '移除' })).toBeEnabled()
  })

  it('在线编辑副本的 SKILL.md', async () => {
    let content: unknown = null
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      agentSkills: [copy()],
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/skills/as-1`), () => HttpResponse.json({ ...copy(), files: [], skillMd: '---\nname: pdf-tools\n---\n旧' })),
        http.put(apiUrl(`/agents/${agent.id}/skills/as-1/skill-md`), async ({ request }) => {
          content = await request.json()
          return HttpResponse.json({ ...copy(), files: [], skillMd: '' })
        }),
      ],
    })
    await user.click(await within(region('Skills')).findByRole('button', { name: '编辑 SKILL.md' }))
    const editor = await screen.findByLabelText('SKILL.md')
    await waitFor(() => expect(editor).toHaveValue('---\nname: pdf-tools\n---\n旧'))
    await user.type(editor, '新')
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(content).toEqual({ content: '---\nname: pdf-tools\n---\n旧新' }))
  })
})

describe('可用 CLI', () => {
  it('添加没有模板的 CLI：只作标记，没有凭证录入', async () => {
    let added: string | null = null
    const agent = makeAgent()
    let current = agent
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.put(apiUrl(`/agents/${agent.id}/clis/:cli`), ({ params }) => {
          added = params.cli as string
          current = { ...agent, clis: [{ name: 'jq', hasTemplate: false, credential: null }] }
          return HttpResponse.json(current.clis[0])
        }),
      ],
    })
    const clis = region('可用 CLI')
    await user.type(within(clis).getByLabelText('CLI 名称'), 'jq')
    await user.click(within(clis).getByRole('button', { name: '添加 CLI' }))
    await waitFor(() => expect(added).toBe('jq'))
    const item = await within(clis).findByRole('listitem', { name: 'jq' })
    expect(item).toHaveTextContent('只作标记，不安装，不注入凭证')
    expect(within(item).queryByRole('button', { name: /凭证/ })).toBeNull()
  })

  it('网络错误时保留输入并显示无法连接', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [http.put(apiUrl(`/agents/${agent.id}/clis/:cli`), () => HttpResponse.error())] })
    const clis = region('可用 CLI')
    await user.type(within(clis).getByLabelText('CLI 名称'), 'gh')
    await user.click(within(clis).getByRole('button', { name: '添加 CLI' }))
    expect(await within(clis).findByText('无法连接服务端')).toBeInTheDocument()
    expect(within(clis).getByLabelText('CLI 名称')).toHaveValue('gh')
  })

  it('名称不合法时不提交', async () => {
    const { user } = await renderConfig()
    const clis = region('可用 CLI')
    await user.type(within(clis).getByLabelText('CLI 名称'), 'Bad Name')
    await user.click(within(clis).getByRole('button', { name: '添加 CLI' }))
    expect(await within(clis).findByText(/只能使用小写字母/)).toBeInTheDocument()
  })

  it('移除项目 CLI 保留共享账号及已有任务权限', async () => {
    const { user } = await renderConfig({ agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] }) })
    const item = within(region('可用 CLI')).getByRole('listitem', { name: 'gh' })
    await user.click(within(item).getByRole('button', { name: '移除' }))
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('项目共享账号保留。已有任务继续沿用启动时的权限和凭证。')
  })
})

describe('凭证只写不读', () => {
  it('已录入的 gh 凭证只显示状态与元数据', async () => {
    await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ envNames: ['GH_TOKEN'] }) }] }) })
    const item = within(region('可用 CLI')).getByRole('listitem', { name: 'gh' })
    expect(within(item).getByText('已设置')).toBeInTheDocument()
    expect(within(item).getByText('注入的环境变量：GH_TOKEN')).toBeInTheDocument()
    expect(within(item).getByText(/更新于/)).toBeInTheDocument()
    expect(await within(item).findByText('内置模板 v2.100.0')).toBeInTheDocument()
  })

  it('录入 lark-cli 凭证：遮蔽输入，品牌默认 feishu，保存后清空，明文不留在页面与存储中', async () => {
    let body: unknown = null
    const agent = makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'lark-cli', hasTemplate: true, credential: null }] })
    let current = agent
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.put(apiUrl(`/agents/${agent.id}/clis/lark-cli/credential`), async ({ request }) => {
          body = await request.json()
          current = { ...agent, clis: [{ name: 'lark-cli', hasTemplate: true, credential: credentialMeta({ filePaths: ['.lark-cli/config.json'], warnings: larkTemplate.warnings }) }] }
          return HttpResponse.json(current.clis[0].credential)
        }),
      ],
    })
    const item = within(region('可用 CLI')).getByRole('listitem', { name: 'lark-cli' })
    expect((await within(item).findAllByRole('note'))[0]).toHaveTextContent('宿主用户本人的飞书身份')
    await user.click(within(item).getByRole('button', { name: '录入凭证' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByLabelText('App Secret')).toHaveAttribute('type', 'password')
    expect(within(dialog).getByLabelText('品牌')).toHaveValue('feishu')
    await user.type(within(dialog).getByLabelText('App ID'), 'cli_app_id')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    expect(await within(dialog).findByText('请填写此项')).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('App Secret'), 'super-secret-value')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(body).toEqual({ fields: { appId: 'cli_app_id', appSecret: 'super-secret-value', brand: 'feishu' } }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await within(item).findByText('已设置')).toBeInTheDocument()
    expect(document.body.innerHTML).not.toContain('super-secret-value')
    expect(router.state.location.pathname + router.state.location.search).not.toContain('super-secret-value')
    expect(JSON.stringify({ ...localStorage })).not.toContain('super-secret-value')
    expect(JSON.stringify({ ...sessionStorage })).not.toContain('super-secret-value')
  })

  it('凭证不可用时提示重新录入', async () => {
    await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'gh', hasTemplate: true, credential: credentialMeta({ status: 'unavailable' }) }] }) })
    expect(within(region('可用 CLI')).getByText('不可用，需要重新录入')).toBeInTheDocument()
  })


})

describe('删除 Agent', () => {
  it('空闲时确认后删除，回到列表', async () => {
    let deleted = false
    const agent = makeAgent()
    const { user, router } = await renderConfig({
      agent,
      handlers: [
        // 删除后列表里不再有它：项目没有 Agent 了，Agent 页停在首次使用的空状态，不跳到别的 Agent。
        http.get(agentsUrl, () => HttpResponse.json({ agents: deleted ? [] : [toListItem(agent)] })),
        http.delete(apiUrl(`/agents/${agent.id}`), () => {
          deleted = true
          return new HttpResponse(null, { status: 204 })
        }),
      ],
    })
    const danger = region('删除 Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: '删除这个 Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: '删除这个 Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('对话历史保留备查')
    expect(confirm).toHaveTextContent('项目共享账号保留')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(deleted).toBe(true))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
  })

  it('有排队消息时不可删除并说明原因', async () => {
    await renderConfig({ status: { busy: true, queued: { interrupt: 0, normal: 1 } } })
    const danger = region('删除 Agent')
    await waitFor(() => expect(within(danger).getByText(/只有在 Agent 空闲时才能删除/)).toBeInTheDocument())
    expect(within(danger).getByRole('button', { name: '删除这个 Agent' })).toBeDisabled()
  })

  it('删除时恰好变忙，显示原因', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [http.delete(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('agent_not_idle', 'busy'), { status: 409 }))],
    })
    const danger = region('删除 Agent')
    await waitFor(() => expect(within(danger).getByRole('button', { name: '删除这个 Agent' })).toBeEnabled())
    await user.click(within(danger).getByRole('button', { name: '删除这个 Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    expect(await within(confirm).findByText(/Agent 不空闲/)).toBeInTheDocument()
  })
})
