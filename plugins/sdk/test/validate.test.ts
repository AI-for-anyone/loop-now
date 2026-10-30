// 校验库测试：官方插件通过；常见错误逐项报出；只有 pi 段的标准 Pi 包按 npm 名得到 id。

import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { after, describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'

import { validatePlugin, versionAtLeast } from '../src/validate.ts'

const official = fileURLToPath(new URL('../../official/', import.meta.url))
const tmp = mkdtempSync(path.join(tmpdir(), 'loop-now-validate-'))
after(() => rmSync(tmp, { recursive: true, force: true }))

function plugin(name: string, pkg: unknown, files: Record<string, string> = {}): string {
  const dir = path.join(tmp, name)
  mkdirSync(dir, { recursive: true })
  writeFileSync(path.join(dir, 'package.json'), typeof pkg === 'string' ? pkg : JSON.stringify(pkg))
  for (const [f, content] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dir, f)), { recursive: true })
    writeFileSync(path.join(dir, f), content)
  }
  return dir
}

describe('validatePlugin', () => {
  for (const id of ['webhook', 'hello', 'playwright']) {
    it(`官方插件 ${id} 没有错误也没有提醒`, () => {
      const r = validatePlugin(path.join(official, id))
      assert.deepEqual(r.findings, [])
      assert.equal(r.ok, true)
      assert.equal(r.manifest!.id, id)
    })
  }

  it('报出清单里的每个错误', () => {
    const dir = plugin(
      'bad',
      {
        name: 'bad',
        version: 'not ok!',
        loopNow: {
          id: 'Bad_ID',
          minPlatformVersion: '2027.01',
          events: ['run.ended', 'run.ended', 'nope.event'],
          permissions: ['state', 'root'],
          configSchema: { type: 'array', properties: { s: { type: 'number', 'x-secret': true }, p: { 'x-scope': 'global' } } },
          accounts: [{ kind: 'Lark' }],
          actions: [{ id: 'go', label: '', where: ['agent'] }, { id: 'ok', label: 'OK', where: ['nowhere'] }],
          schedule: [{ id: 'sweep', cron: '* * *' }],
          mcpServers: { browser: { args: [] } },
          bin: ['../outside'],
        },
      },
      { 'README.md': '' },
    )
    const r = validatePlugin(dir, { platformVersion: '2026.11' })
    assert.equal(r.ok, false)
    const errors = r.findings.filter((f) => f.level === 'error').map((f) => f.path)
    assert.deepEqual(errors, [
      'version',
      'loopNow.id',
      'loopNow.minPlatformVersion',
      'loopNow.events[1]',
      'loopNow.events[2]',
      'loopNow.permissions[1]',
      'loopNow.platform',
      'loopNow.bin[0]',
      'loopNow.mcpServers.browser',
      'loopNow.configSchema.type',
      'loopNow.configSchema.properties.s.x-secret',
      'loopNow.configSchema.properties.p.x-scope',
      'loopNow.accounts[0]',
      'loopNow.actions[0]',
      'loopNow.actions[1].where',
      'loopNow.schedule[0]',
    ])
  })

  it('mcpServers 接受 stdio 与 streamable HTTP 条目（与 Pi 选传输的方式相同），其余报错', () => {
    const good = {
      stdio: { command: 'node', args: ['./mcp/server.js'] },
      typed_stdio: { type: 'stdio', command: 'node' },
      http: { url: 'http://127.0.0.1:3845/mcp', description: '本机服务' },
      https: { type: 'streamable-http', url: 'https://mcp.example.com/mcp', oauth: { clientName: 'Loop Now' } },
      typed_http: { type: 'http', url: 'https://mcp.example.com/mcp' },
      both: { url: 'https://mcp.example.com/mcp', command: 'node' },
    }
    const ok = validatePlugin(plugin('mcp-ok', { name: 'mcp-ok', version: '1.0.0', description: 'MCP', keywords: ['loop-now-plugin'], loopNow: { id: 'mcp-ok', displayName: 'MCP', mcpServers: good } }, { 'README.md': '' }))
    assert.deepEqual(ok.findings, [])
    assert.deepEqual(ok.manifest!.mcpServers, Object.keys(good).sort())

    const bad = {
      sse: { type: 'sse', url: 'https://mcp.example.com/sse' },
      ws: { type: 'ws', url: 'wss://mcp.example.com/mcp' },
      file_url: { url: 'file:///tmp/mcp' },
      not_url: { url: 'mcp.example.com/mcp' },
      no_slashes: { url: 'http:mcp.example.com/mcp' },
      no_host: { url: 'http:///mcp' },
      bad_port: { url: 'http://127.0.0.1:99999/mcp' },
      empty_command: { command: '' },
      http_without_url: { type: 'http', command: 'node' },
      stdio_without_command: { type: 'stdio', url: 'https://mcp.example.com/mcp' },
      nothing: { args: [] },
      not_object: 'node',
      Bad_Name: { command: 'node' },
    }
    const r = validatePlugin(plugin('mcp-bad', { name: 'mcp-bad', version: '1.0.0', description: 'MCP', keywords: ['loop-now-plugin'], loopNow: { id: 'mcp-bad', displayName: 'MCP', mcpServers: bad } }, { 'README.md': '' }))
    assert.equal(r.ok, false)
    assert.deepEqual(
      r.findings.map((f) => [f.level, f.path]),
      Object.keys(bad).map((k) => ['error', `loopNow.mcpServers.${k}`]),
    )
    assert.match(r.findings[0]!.message, /"sse" 不支持 SSE，请用 streamable HTTP 的 url/)
    assert.match(r.findings[1]!.message, /"ws" 的 type 只能是 stdio、http 或 streamable-http/)
    assert.match(r.findings[2]!.message, /"file_url" 须是带 command 的 stdio 条目或带 http\/https url 的 streamable HTTP 条目/)
  })

  it('不是插件的目录与坏 JSON', () => {
    assert.match(validatePlugin(plugin('plain', { name: 'x', version: '1.0.0' })).findings[0]!.message, /不是插件/)
    assert.match(validatePlugin(plugin('broken', '{')).findings[0]!.message, /不是合法的 JSON/)
    assert.match(validatePlugin(path.join(tmp, 'absent')).findings[0]!.message, /不是目录/)
    assert.match(validatePlugin(plugin('empty', { name: 'x', version: '1.0.0', loopNow: { id: 'x' } })).findings[0]!.message, /既没有 Agent 半边/)
  })

  it('description 与 loopNow.displayName 可以是字符串或按语言的对象；对象须至少一项、键为语言标签、值非空', () => {
    const name = { 'zh-CN': '示例', 'en-US': 'Example' }
    const description = { 'zh-CN': '一个示例插件', en: 'An example plugin', 'zh-Hant-TW': '一個範例外掛' }
    const ok = validatePlugin(plugin('i18n-ok', { name: 'i18n-ok', version: '1.0.0', description, keywords: ['loop-now-plugin'], loopNow: { id: 'i18n-ok', displayName: name, mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
    assert.deepEqual(ok.findings, [])
    assert.deepEqual(ok.manifest!.displayName, name)
    assert.deepEqual(ok.manifest!.description, description)

    const bad: [string, unknown][] = [
      ['empty', {}],
      ['bad_key', { zh_CN: '示例' }],
      ['long_key', { 'en-US-abcdefghi': 'x' }],
      ['blank_value', { 'zh-CN': '  ' }],
      ['number_value', { 'zh-CN': 1 }],
      ['number', 42],
      ['array', ['示例']],
      ['null', null],
      ['boolean', true],
    ]
    for (const [label, value] of bad) {
      const r = validatePlugin(plugin(`i18n-${label}`, { name: 'x', version: '1.0.0', description: value, keywords: ['loop-now-plugin'], loopNow: { id: 'x', displayName: value, mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
      assert.equal(r.ok, false, label)
      assert.deepEqual(
        r.findings.filter((f) => f.level === 'error').map((f) => [f.path, f.message]),
        [
          ['description', 'description 须是字符串，或以语言标签（如 zh-CN、en-US）为键、非空字符串为值的对象，至少一项'],
          ['loopNow.displayName', 'loopNow.displayName 须是字符串，或以语言标签（如 zh-CN、en-US）为键、非空字符串为值的对象，至少一项'],
        ],
        label,
      )
    }

    // 字符串照旧：空或缺省只提醒，displayName 回落到 npm 名。
    const plain = validatePlugin(plugin('i18n-plain', { name: 'plain-name', version: '1.0.0', description: '', keywords: ['loop-now-plugin'], loopNow: { id: 'plain', displayName: '', mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
    assert.equal(plain.ok, true)
    assert.equal(plain.manifest!.displayName, 'plain-name')
    assert.deepEqual(plain.findings.map((f) => f.path), ['description', 'loopNow.displayName'])
  })

  it('只有 pi 段的 Pi 包按 npm 名得到 id；提醒依赖放法', () => {
    const dir = plugin(
      'pi-only',
      {
        name: '@someone/Pi-Tools.Extra',
        version: '0.3.0',
        description: 'x',
        keywords: ['pi-package'],
        dependencies: { '@earendil-works/pi-coding-agent': '1.4.0', leftpad: '1' },
        pi: { extensions: ['./ext.ts'] },
      },
      { 'ext.ts': 'export default () => {}', 'README.md': '' },
    )
    const r = validatePlugin(dir)
    assert.equal(r.ok, true)
    assert.equal(r.manifest!.id, 'pi-tools-extra')
    assert.deepEqual(
      r.findings.map((f) => f.path),
      ['dependencies.@earendil-works/pi-coding-agent', 'dependencies'],
    )
  })

  it('代管外部身份的渠道须有 identities.manage', () => {
    const pkg = (permissions: string[]) => ({
      name: 'x', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'],
      loopNow: { id: 'x', displayName: 'X', platform: './p.ts', permissions, channels: [{ id: 'mail', label: '邮件', manageIdentities: true }, { id: 'chat', label: '聊天', manageIdentities: 'yes' }] },
    })
    const bad = validatePlugin(plugin('identity-bad', pkg([]), { 'p.ts': '', 'README.md': '' }))
    assert.deepEqual(bad.findings.map((f) => f.path), ['loopNow.channels[0].manageIdentities', 'loopNow.channels[1].manageIdentities'])
    const good = validatePlugin(plugin('identity-good', { ...pkg(['identities.manage']), loopNow: { ...pkg(['identities.manage']).loopNow, channels: [{ id: 'mail', label: '邮件', manageIdentities: true }] } }, { 'p.ts': '', 'README.md': '' }))
    assert.deepEqual(good.findings, [])
    assert.deepEqual(good.manifest!.identityChannels, ['mail'])
  })

  it('平台版本比较', () => {
    assert.equal(versionAtLeast('2026.11', '2026.11'), true)
    assert.equal(versionAtLeast('2026.12.1', '2026.11'), true)
    assert.equal(versionAtLeast('2026.10', '2026.11'), false)
    assert.equal(versionAtLeast('dev', '2030.01'), true)
  })
})
