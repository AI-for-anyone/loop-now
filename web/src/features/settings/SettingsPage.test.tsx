import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'

describe('全局设置页草稿导航', () => {
  it('模型配置加载后可编辑，未保存时离开需要确认', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/settings')
    const editor = await screen.findByLabelText('models.json')
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')
    await user.click(within(sidebar()).getByRole('link', { name: '插件市场' }))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings')
    expect(editor.textContent?.trim()).toBe('{"providers":{}}')
    expect(editor.textContent).not.toBe('{"providers":{}}')
  })
})

// 改版“一张印出来的纸”第 4 步：全局设置排在一张纸上，编号分区与票根里的目录尺。
describe('纸张上的全局设置', () => {
  const titles = ['界面语言', '本机模式', 'Agent 运行时', '模型配置', '全局文件夹', '服务端', 'CLI 模板']
  const region = (name: string) => screen.getByRole('region', { name })
  const open = async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    return screen.findByRole('heading', { level: 1, name: '全局设置' })
  }

  it('页头是页面标题：面包屑“全局›全局设置”，旁边是服务端版本与连接状态；本页可改什么写在一张黄色便签上', async () => {
    const title = await open()
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: '页面位置' })).toHaveTextContent('全局›全局设置')
    expect(await within(header).findByText('服务端连接正常')).toBeInTheDocument()
    const note = screen.getByText('模型配置、本机模式与文件夹可在这里修改；服务端的环境配置保持只读。').closest('[role="note"]')!
    expect(note).toHaveClass('riso-tilt')
  })

  it('七个分区按顺序编号，直接排在纸上；票根里的目录尺列出同样的七项；本机模式一区的地址仍是 #local-mode', async () => {
    await open()
    await screen.findByRole('region', { name: '服务端' })
    const sheet = region('界面语言').closest('[data-sheet]')!
    titles.forEach((name, index) => {
      const section = region(name)
      expect(sheet).toContainElement(section)
      expect(section.querySelector('[data-section-number]')).toHaveTextContent(String(index + 1).padStart(2, '0'))
    })
    expect(region('本机模式')).toHaveAttribute('id', 'local-mode')
    const toc = screen.getByRole('navigation', { name: '目录' })
    expect(within(toc).getAllByRole('link').map((link) => link.textContent)).toEqual(titles.map((name, index) => `${String(index + 1).padStart(2, '0')}${name}`))
    expect(within(toc).getByRole('link', { name: '本机模式' })).toHaveAttribute('href', '#local-mode')
  })

  it('界面语言是分段按钮，当前语言是黄色', async () => {
    await open()
    const group = within(region('界面语言')).getByRole('radiogroup', { name: '界面语言' })
    expect(group).toHaveClass('border-2', 'border-ink', 'rounded-full')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveAttribute('aria-checked', 'true')
    expect(within(group).getByRole('radio', { name: '中文' })).toHaveClass('data-[state=on]:bg-riso-yellow')
  })

  it('本机模式的四个开关一行一项，行间虚线；打开的开关是黄底藏青圆钮，旁边写着“开”或“关”', async () => {
    await open()
    const local = region('本机模式')
    const switches = await within(local).findAllByRole('switch')
    expect(switches).toHaveLength(4)
    for (const item of switches) {
      expect(item).toHaveClass('data-[state=checked]:bg-riso-yellow', 'border-2', 'border-ink')
      expect(item.closest('li')).toHaveClass('border-b-[1.5px]', 'border-dashed')
      expect(item.nextElementSibling).toHaveTextContent(item.getAttribute('aria-checked') === 'true' ? '开' : '关')
    }
  })

  it('Agent 运行时每个一张卡片：2px 藏青描边、藏青实色投影，名称是展示字', async () => {
    await open()
    const card = await within(region('Agent 运行时')).findByRole('region', { name: 'Claude Code' })
    expect(card).toHaveClass('border-2', 'border-ink', 'bg-white', 'shadow-[5px_5px_0_var(--ink)]')
    expect(within(card).getByText('Claude Code')).toHaveClass('font-display-cn')
  })

  it('全局文件夹一区：说明加一个 2px 藏青描边的文件管理器，不再是卡片', async () => {
    await open()
    const folder = region('全局文件夹')
    expect(folder.querySelector('[data-file-manager]')).toHaveClass('border-2', 'border-ink')
    expect(folder).not.toHaveClass('shadow-print-rest')
  })

  it('服务端一区写着默认组织、默认项目、API 地址、版本与连接状态', async () => {
    await open()
    const server = region('服务端')
    for (const label of ['默认组织', '默认项目', 'API 地址', '版本', '连接状态']) expect(within(server).getByText(label)).toBeInTheDocument()
  })
})
