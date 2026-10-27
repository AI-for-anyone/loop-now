import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { agentParty, consoleParty, externalParty, makeIdentity, makeInboxMessage, makeToken, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const SECRET = 'lne_0a1b2c3d_5Qm8vR2kT9wXc4LpZ7nB3sYh6JdF1aGe'

interface Backend {
  identities: Schemas['ExternalIdentity'][]
  tokens: Record<string, Schemas['ExternalToken'][]>
  inbox?: Record<string, Schemas['InboxMessage'][]>
}

// 假的外部身份后端：列表只含未删除的，单个查询含已删除的；记录写操作。
function identityBackend(backend: Backend) {
  const writes: { method: string; path: string; body?: unknown }[] = []
  const handlers = [
    http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: backend.identities.filter((i) => !i.deletedAt) })),
    http.post(apiUrl('/external-identities'), async ({ request }) => {
      const body = (await request.json()) as { name: string }
      writes.push({ method: 'POST', path: '/external-identities', body })
      if (backend.identities.some((i) => i.name === body.name && !i.deletedAt)) {
        return HttpResponse.json(errorBody('name_conflict', 'name already used'), { status: 409 })
      }
      const identity = makeIdentity({ name: body.name, createdAt: new Date().toISOString() })
      backend.identities.push(identity)
      backend.tokens[identity.id] = []
      return HttpResponse.json(identity, { status: 201 })
    }),
    http.get(apiUrl('/external-identities/:id'), ({ params }) => {
      const identity = backend.identities.find((i) => i.id === params.id)
      return identity ? HttpResponse.json(identity) : HttpResponse.json(errorBody('not_found'), { status: 404 })
    }),
    http.delete(apiUrl('/external-identities/:id'), ({ params }) => {
      writes.push({ method: 'DELETE', path: `/external-identities/${params.id}` })
      const identity = backend.identities.find((i) => i.id === params.id)!
      identity.deletedAt = new Date().toISOString()
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/external-identities/:id/tokens'), ({ params }) => HttpResponse.json({ tokens: backend.tokens[String(params.id)] ?? [] })),
    http.post(apiUrl('/external-identities/:id/tokens'), async ({ params, request }) => {
      const body = (await request.json()) as { expiresAt?: string }
      writes.push({ method: 'POST', path: `/external-identities/${params.id}/tokens`, body })
      const token = makeToken({ createdAt: new Date().toISOString(), expiresAt: body.expiresAt ?? null })
      backend.tokens[String(params.id)] = [token, ...(backend.tokens[String(params.id)] ?? [])]
      return HttpResponse.json({ token, secret: SECRET }, { status: 201 })
    }),
    http.delete(apiUrl('/external-identities/:id/tokens/:tokenId'), ({ params }) => {
      writes.push({ method: 'DELETE', path: `/external-identities/${params.id}/tokens/${params.tokenId}` })
      const token = backend.tokens[String(params.id)].find((k) => k.id === params.tokenId)!
      token.revokedAt = new Date().toISOString()
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/external-identities/:id/inbox'), ({ params, request }) => {
      const url = new URL(request.url)
      const direction = url.searchParams.get('direction') ?? 'all'
      const unread = url.searchParams.get('unread') === 'true'
      const list = (backend.inbox?.[String(params.id)] ?? []).filter((m) => {
        const received = m.to.id === params.id
        if (direction === 'received' && !received) return false
        if (direction === 'sent' && received) return false
        return !unread || !m.readAt
      })
      return HttpResponse.json({ messages: list.map(summaryOf), nextCursor: null })
    }),
    http.get(apiUrl('/external-identities/:id/inbox/:messageId'), ({ params }) =>
      HttpResponse.json(backend.inbox?.[String(params.id)]?.find((m) => m.id === params.messageId)),
    ),
  ]
  return { handlers, writes }
}

async function renderIdentities(backend: Backend, path = '/identities') {
  const { handlers, writes } = identityBackend(backend)
  server.use(...handlers, ...baseHandlers([]))
  const user = userEvent.setup()
  const result = renderRoutes(routes, path)
  await screen.findByRole('heading', { name: /^外部身份/, level: 1 })
  return { ...result, user, writes }
}

const list = () => within(screen.getByRole('complementary', { name: '外部身份列表' }))
const tokenTable = async () => within(await screen.findByRole('table'))

describe('创建外部身份', () => {
  it('还没有外部身份时说明为空；创建 ci-bot 后它出现在列表中并被选中', async () => {
    const { user, writes, router } = await renderIdentities({ identities: [], tokens: {} })
    expect(await screen.findByText('还没有外部身份。')).toBeInTheDocument()
    await user.click(list().getByRole('button', { name: '新建' }))
    const dialog = within(await screen.findByRole('dialog', { name: '新建外部身份' }))
    expect(dialog.getByText(/不能是 console 或 platform/)).toBeInTheDocument()
    await user.type(dialog.getByRole('textbox', { name: '名称' }), 'ci-bot')
    await user.click(dialog.getByRole('button', { name: '创建' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'POST', path: '/external-identities', body: { name: 'ci-bot' } }]))
    await waitFor(() => expect(list().getByRole('link', { name: /ci-bot/ })).toHaveAttribute('aria-current', 'page'))
    expect(router.state.location.pathname).toMatch(/^\/identities\/.+/)
    expect(await screen.findByRole('heading', { name: 'ci-bot', level: 2 })).toBeInTheDocument()
  })

  it('名称不满足规则或是保留名称时不提交，并说明原因', async () => {
    const { user, writes } = await renderIdentities({ identities: [], tokens: {} })
    await user.click(list().getByRole('button', { name: '新建' }))
    const dialog = within(await screen.findByRole('dialog', { name: '新建外部身份' }))
    const input = dialog.getByRole('textbox', { name: '名称' })
    await user.type(input, 'platform')
    await user.click(dialog.getByRole('button', { name: '创建' }))
    expect(dialog.getByRole('alert')).toHaveTextContent('名称不能是 platform，这是保留名称')
    await user.clear(input)
    await user.type(input, 'CI Bot')
    await user.click(dialog.getByRole('button', { name: '创建' }))
    expect(dialog.getByRole('alert')).toHaveTextContent('名称只能使用小写字母')
    expect(writes).toEqual([])
  })

  it('名称重复：在名称旁显示原因，填写的内容保持不变', async () => {
    const { user } = await renderIdentities({ identities: [makeIdentity({ name: 'ci-bot' })], tokens: {} })
    await user.click(await list().findByRole('button', { name: '新建' }))
    const dialog = within(await screen.findByRole('dialog', { name: '新建外部身份' }))
    await user.type(dialog.getByRole('textbox', { name: '名称' }), 'ci-bot')
    await user.click(dialog.getByRole('button', { name: '创建' }))
    expect(await dialog.findByRole('alert')).toHaveTextContent('名称已被占用')
    expect(dialog.getByRole('textbox', { name: '名称' })).toHaveValue('ci-bot')
  })
})

describe('删除外部身份', () => {
  it('确认框列出四条后果；删除后它从列表中消失，之前与它的线索仍可查看，对方显示为已删除身份', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const backend: Backend = { identities: [ciBot, makeIdentity({ name: 'deploy-watcher' })], tokens: { [ciBot.id]: [makeToken(), makeToken()] } }
    const { user, writes } = await renderIdentities(backend, `/identities/${ciBot.id}`)
    expect(await list().findByRole('link', { name: /ci-bot/ })).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: '删除外部身份' }))
    const confirm = within(await screen.findByRole('alertdialog', { name: '删除外部身份「ci-bot」？' }))
    expect(confirm.getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      '它的全部令牌会同时吊销；',
      '它发过、收过的消息保留，显示为已删除身份；',
      '之后发给它的消息会被拒绝；',
      '名称不再占用，可以重新创建同名身份。',
    ])
    await user.click(confirm.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', path: `/external-identities/${ciBot.id}` }]))
    await waitFor(() => expect(list().queryByRole('link', { name: /ci-bot/ })).toBeNull())
    expect(list().getByRole('link', { name: /deploy-watcher/ })).toBeInTheDocument()
  })

  it('已删除的身份：只显示只读收件箱与删除时间，不请求令牌，也不能签发、吊销或删除', async () => {
    const gone = makeIdentity({ name: 'release-bot', deletedAt: '2026-10-11T02:00:00Z' })
    const inbox = { [gone.id]: [makeInboxMessage({ from: externalParty(gone, true), to: consoleParty, text: 'v0.1.0 已发布', readAt: '2026-10-11T01:00:00Z' })] }
    await renderIdentities({ identities: [gone], tokens: {}, inbox }, `/identities/${gone.id}`)
    expect(await screen.findByRole('heading', { name: 'release-bot', level: 2 })).toBeInTheDocument()
    expect(screen.getByText('已删除身份')).toBeInTheDocument()
    expect(screen.getByText(/^删除于/)).toBeInTheDocument()
    expect(await screen.findByText('v0.1.0 已发布')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '签发令牌' })).toBeNull()
    expect(screen.queryByRole('button', { name: '删除外部身份' })).toBeNull()
    expect(screen.queryByRole('table')).toBeNull()
  })
})

describe('签发令牌', () => {
  it('签发并复制：明文与风险说明只显示一次，关闭前确认；关闭后页面、地址、浏览器存储与查询缓存中都没有明文', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user, writes, client, router } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}`)
    expect(await screen.findByRole('note')).toHaveTextContent(/令牌拥有外部 CLI 的全部权限.*泄露令牌等于把平台的控制权交给对方/)
    await user.click(screen.getByRole('button', { name: '签发令牌' }))
    const issue = within(await screen.findByRole('dialog', { name: '为 ci-bot 签发令牌' }))
    expect(issue.getByRole('radio', { name: '30 天' })).toBeChecked()
    expect(issue.getByText(/^到期时间：/)).toBeInTheDocument()
    expect(issue.getByText(/令牌拥有外部 CLI 的全部权限/)).toBeInTheDocument()
    const before = Date.now()
    await user.click(issue.getByRole('button', { name: '签发' }))
    const result = within(await screen.findByRole('dialog', { name: '令牌已签发' }))
    const expiresAt = (writes[0].body as { expiresAt: string }).expiresAt
    const days = (Date.parse(expiresAt) - before) / 86_400_000
    expect(days).toBeGreaterThan(29.99)
    expect(days).toBeLessThan(30.01)
    expect(result.getByText(SECRET)).toBeInTheDocument()
    expect(result.getByRole('alert')).toHaveTextContent('这是唯一一次显示令牌明文。关闭后无法再查看，请现在复制并妥善保存。')
    expect(result.getByText(/令牌拥有外部 CLI 的全部权限/)).toBeInTheDocument()
    await user.click(result.getByRole('button', { name: '复制' }))
    await expect(navigator.clipboard.readText()).resolves.toBe(SECRET)
    // 关闭前要确认；选“返回”时明文仍在。
    await user.click(result.getByRole('button', { name: '我已保存，关闭' }))
    const confirm = within(await screen.findByRole('alertdialog', { name: '确认已保存令牌？' }))
    expect(confirm.getByText('关闭后，这个令牌的明文不会再显示，也无法找回。')).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: '返回' }))
    expect(screen.getByText(SECRET)).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '确认关闭' }))
    await waitFor(() => expect(screen.queryByText(SECRET)).toBeNull())
    expect(document.body.innerHTML).not.toContain(SECRET)
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(SECRET)
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(SECRET)
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(SECRET)
    expect(JSON.stringify({ ...window.sessionStorage })).not.toContain(SECRET)
    expect(router.state.location.pathname + router.state.location.search + router.state.location.hash).not.toContain(SECRET)
    // 再次查看令牌列表：有这个令牌的元数据，没有明文。
    const table = await tokenTable()
    expect(table.getAllByRole('row')).toHaveLength(2)
    expect(table.getByText('有效')).toBeInTheDocument()
    expect(table.queryByText(SECRET)).toBeNull()
  })

  it('改选“永不过期”时不提交到期时间；指定日期取当天结束时刻，早于今天的日期不提交', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user, writes } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}`)
    await user.click(await screen.findByRole('button', { name: '签发令牌' }))
    let issue = within(await screen.findByRole('dialog', { name: '为 ci-bot 签发令牌' }))
    await user.click(issue.getByRole('radio', { name: '永不过期' }))
    expect(issue.getByText('令牌不会自动失效，只能手动吊销。')).toBeInTheDocument()
    await user.click(issue.getByRole('button', { name: '签发' }))
    await waitFor(() => expect(writes.at(-1)?.body).toEqual({}))
    await user.click(within(await screen.findByRole('dialog', { name: '令牌已签发' })).getByRole('button', { name: '我已保存，关闭' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '确认关闭' }))
    await user.click(await screen.findByRole('button', { name: '签发令牌' }))
    issue = within(await screen.findByRole('dialog', { name: '为 ci-bot 签发令牌' }))
    await user.click(issue.getByRole('radio', { name: '指定日期' }))
    const date = issue.getByLabelText('到期日期（当天结束时失效）')
    await user.type(date, '2020-01-01')
    await user.click(issue.getByRole('button', { name: '签发' }))
    expect(issue.getByRole('alert')).toHaveTextContent('请选择今天或以后的日期')
    expect(writes).toHaveLength(1)
    await user.clear(date)
    await user.type(date, '2099-12-31')
    await user.click(issue.getByRole('button', { name: '签发' }))
    await waitFor(() => expect(writes).toHaveLength(2))
    expect(new Date((writes[1].body as { expiresAt: string }).expiresAt).getTime()).toBe(new Date(2099, 11, 31, 23, 59, 59, 999).getTime())
  })
})

describe('查看与吊销令牌', () => {
  it('只显示元数据；吊销其中一个令牌后它显示为已吊销，另一个仍为有效；已过期的令牌显示已过期', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const first = makeToken({ lastUsedAt: '2026-10-12T06:02:00Z' })
    const second = makeToken({ expiresAt: '2099-01-01T00:00:00Z' })
    const old = makeToken({ expiresAt: '2026-09-08T00:30:00Z' })
    const { user, writes } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [first, second, old] } }, `/identities/${ciBot.id}`)
    const table = await tokenTable()
    expect(table.getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['标识', '签发时间', '过期时间', '最后使用', '状态', '操作'])
    const rowOf = (prefix: string) => table.getByText(prefix).closest('tr')!
    expect(rowOf(first.prefix)).toHaveTextContent(/永不过期.*有效/)
    expect(rowOf(old.prefix)).toHaveTextContent(/从未使用.*已过期/)
    expect(within(rowOf(old.prefix)).queryByRole('button')).toBeNull()
    expect(list().getByRole('link', { name: /ci-bot/ })).toHaveTextContent('2 个有效令牌')
    await user.click(within(rowOf(first.prefix)).getByRole('button', { name: `吊销 ${first.prefix}` }))
    const confirm = within(await screen.findByRole('alertdialog', { name: `吊销令牌 ${first.prefix}？` }))
    expect(confirm.getByText(/ci-bot 的其他令牌不受影响/)).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: '吊销' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', path: `/external-identities/${ciBot.id}/tokens/${first.id}` }]))
    await waitFor(() => expect(rowOf(first.prefix)).toHaveTextContent('已吊销'))
    expect(rowOf(second.prefix)).toHaveTextContent('有效')
    expect(within(rowOf(second.prefix)).getByRole('button', { name: `吊销 ${second.prefix}` })).toBeInTheDocument()
  })
})

describe('外部身份的收件箱只读', () => {
  it('列出收到和发出的消息，标明方向与已读；可以按方向与未读筛选；查看全文不改变已读状态', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const reviewer = makeAgent({ name: '代码审查助手', address: '测试组织/测试项目/代码审查助手' })
    const gotIt = makeInboxMessage({ from: agentParty(reviewer), to: externalParty(ciBot), text: '修复已合并，麻烦重跑一次夜间构建。' })
    const sent = makeInboxMessage({ from: externalParty(ciBot), to: consoleParty, text: '夜间构建失败：naming 有 1 个用例未通过。', readAt: '2026-10-12T09:00:00Z' })
    const { user } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] }, inbox: { [ciBot.id]: [gotIt, sent] } }, `/identities/${ciBot.id}`)
    // 换筛选条件时列表重新加载，每次都重新查找。
    const inboxList = () => within(screen.getByRole('list', { name: '收件箱' }))
    await screen.findByRole('list', { name: '收件箱' })
    const items = inboxList().getAllByRole('listitem')
    expect(items[0]).toHaveTextContent(/^收到代码审查助手.*修复已合并.*未读$/)
    expect(items[1]).toHaveTextContent(/^发出控制台@console.*对方已读$/)
    expect(screen.getByText('只读，不会改变已读状态')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: '发出' }))
    await waitFor(() => expect(inboxList().getAllByRole('listitem')).toHaveLength(1))
    expect(inboxList().getByRole('listitem')).toHaveTextContent('发出')
    await user.click(screen.getByRole('checkbox', { name: '只看未读' }))
    expect(await screen.findByText('没有符合条件的消息。')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: '全部' }))
    await screen.findByRole('list', { name: '收件箱' })
    await user.click(inboxList().getByRole('button', { name: /代码审查助手/ }))
    expect(await inboxList().findByText('修复已合并，麻烦重跑一次夜间构建。', { selector: 'p' })).toBeInTheDocument()
    expect(within(inboxList().getByRole('button', { name: /代码审查助手/ })).getByText('未读')).toBeInTheDocument()
  })
})
