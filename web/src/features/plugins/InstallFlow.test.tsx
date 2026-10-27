import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { makeComponents, makeManifest, makePlugin, makePreview, makeResolvedSource } from '@/test/plugins'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { InstallSourceDialog } from './InstallFlow'

const lark = makePreview({
  trusted: false,
  source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/loop-now-lark-notify', ref: 'v0.3.1', commit: 'abc1234def' }),
  manifest: makeManifest({
    id: 'lark-notify',
    displayName: '飞书通知',
    version: '0.3.1',
    events: ['inbox.received', 'question.asked', 'run.ended'],
    permissions: ['inbox.read', 'inbox.send', 'state'],
    components: makeComponents({ channels: [{ id: 'lark', label: '飞书', manageIdentities: false }] }),
  }),
})

// 预览与安装两个接口：记下每次的请求体。
function handlers({ preview = lark, install }: { preview?: typeof lark; install?: (body: Record<string, unknown>) => Response | undefined } = {}) {
  const previews: unknown[] = [], installs: Record<string, unknown>[] = []
  server.use(
    http.get(apiUrl('/plugin-sources/builtin'), () => HttpResponse.json({ sources: [{ id: 'hello', displayName: 'hello 示例', description: '两半各一个最小例子', version: '1.0.0' }, { id: 'webhook', displayName: 'Webhook 通知', description: '收件箱有新消息时 POST', version: '1.2.0' }] })),
    http.post(apiUrl('/plugin-previews'), async ({ request }) => {
      previews.push(await request.json())
      return HttpResponse.json(preview, { status: 201 })
    }),
    http.post(apiUrl('/plugins'), async ({ request }) => {
      const body = (await request.json()) as Record<string, unknown>
      installs.push(body)
      return install?.(body) ?? HttpResponse.json(makePlugin({ id: preview.manifest.id, displayName: preview.manifest.displayName }), { status: 201 })
    }),
  )
  return { previews, installs }
}

function renderInstall() {
  const onOpenChange = vi.fn(), onInstalled = vi.fn()
  renderWithQuery(<InstallSourceDialog open onOpenChange={onOpenChange} installed={['webhook']} onInstalled={onInstalled} />)
  return { onOpenChange, onInstalled }
}

describe('安装插件：读取清单 → 确认 → 安装', () => {
  it('git 来源：读取清单后展示组件、事件与权限的说明和未审核警告；确认后带 confirm 安装', async () => {
    const user = userEvent.setup()
    const { previews, installs } = handlers()
    const { onInstalled } = renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git 仓库' }))
    await user.type(screen.getByLabelText('仓库地址'), 'https://github.com/foo/loop-now-lark-notify')
    await user.type(screen.getByLabelText('分支、标签或提交（可选）'), 'v0.3.1')
    await user.click(screen.getByRole('button', { name: '读取清单' }))
    const dialog = await screen.findByRole('dialog', { name: /安装 飞书通知/ })
    expect(previews).toEqual([{ source: { kind: 'git', location: 'https://github.com/foo/loop-now-lark-notify', ref: 'v0.3.1' } }])
    expect(within(dialog).getByRole('note')).toHaveTextContent('来自 git，不在任何市场里，没人审核过。')
    expect(within(dialog).getByRole('note')).toHaveTextContent('https://github.com/foo/loop-now-lark-notify @ v0.3.1 (abc1234)')
    expect(within(dialog).getByText('question.asked').parentElement).toHaveTextContent('question.asked · Agent 向你提问')
    expect(within(dialog).getByText('inbox.send').parentElement).toHaveTextContent('inbox.send · 以插件身份发消息')
    expect(within(dialog).getByText('渠道').parentElement).toHaveTextContent('渠道 · 飞书')
    expect(within(dialog).getAllByText('无')[0].parentElement).toHaveTextContent('不会往 Agent 的 Pi 里装任何东西')
    await user.click(within(dialog).getByRole('button', { name: '我看过了，安装' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: lark.previewId, confirm: true }]))
    await waitFor(() => expect(onInstalled).toHaveBeenCalledWith(expect.objectContaining({ id: 'lark-notify' })))
    expect(screen.queryByRole('dialog', { name: /安装 飞书通知/ })).toBeNull()
  })

  it('官方来源：已安装的不能再选；可信的直接安装，不带 confirm', async () => {
    const user = userEvent.setup()
    const hello = makePreview({ manifest: makeManifest({ id: 'hello', displayName: 'hello 示例', version: '1.0.0', components: makeComponents({ agentExtensions: 1, skills: ['skills/hello'] }) }) })
    const { previews, installs } = handlers({ preview: hello })
    renderInstall()
    expect(await screen.findByRole('radio', { name: /Webhook 通知/ })).toBeDisabled()
    await user.click(screen.getByRole('radio', { name: /hello 示例/ }))
    await user.click(screen.getByRole('button', { name: '读取清单' }))
    const dialog = await screen.findByRole('dialog', { name: /安装 hello 示例/ })
    expect(previews).toEqual([{ source: { kind: 'builtin', location: 'hello' } }])
    expect(within(dialog).queryByRole('note')).toBeNull()
    expect(within(dialog).getByText('Pi 扩展 1 个文件').parentElement).toHaveTextContent('授权的 Agent 启动时加载')
    await user.click(within(dialog).getByRole('button', { name: '安装' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: hello.previewId }]))
  })

  it('本地目录要写绝对路径；读取失败时留在来源这一步并显示原因', async () => {
    const user = userEvent.setup()
    handlers()
    server.use(http.post(apiUrl('/plugin-previews'), () => HttpResponse.json(errorBody('plugin_source_failed', 'no package.json'), { status: 422 })))
    const { onOpenChange } = renderInstall()
    await user.click(screen.getByRole('radio', { name: '本地目录' }))
    await user.type(screen.getByLabelText('服务端本机目录的绝对路径'), 'plugins/mine')
    await user.click(screen.getByRole('button', { name: '读取清单' }))
    expect(screen.getByText('请填写绝对路径')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('服务端本机目录的绝对路径'))
    await user.type(screen.getByLabelText('服务端本机目录的绝对路径'), '/Users/me/plugins/mine')
    await user.click(screen.getByRole('button', { name: '读取清单' }))
    expect(await screen.findByText(/插件来源无法获取或依赖安装失败/)).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })

  it('服务端仍要求确认（409 plugin_confirmation_required）时补上警告，再点一次带 confirm 安装', async () => {
    const user = userEvent.setup()
    const trusted = makePreview()
    let first = true
    const { installs } = handlers({
      preview: trusted,
      install: () => {
        if (!first) return undefined
        first = false
        return HttpResponse.json(errorBody('plugin_confirmation_required', 'confirm required'), { status: 409 })
      },
    })
    renderInstall()
    await user.click(screen.getByRole('radio', { name: 'git 仓库' }))
    await user.type(screen.getByLabelText('仓库地址'), 'https://example.com/webhook.git')
    await user.click(screen.getByRole('button', { name: '读取清单' }))
    const dialog = await screen.findByRole('dialog', { name: /安装 Webhook 通知/ })
    await user.click(within(dialog).getByRole('button', { name: '安装' }))
    expect(await within(dialog).findByRole('note')).toHaveTextContent('没人审核过')
    await user.click(within(dialog).getByRole('button', { name: '我看过了，安装' }))
    await waitFor(() => expect(installs).toEqual([{ previewId: trusted.previewId }, { previewId: trusted.previewId, confirm: true }]))
  })
})
