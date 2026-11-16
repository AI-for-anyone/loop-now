// Validation library tests: official plugins pass; common errors are reported one by one; a standard Pi package with only a pi section
// gets its id from the npm name.

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
    it(`official plugin ${id} has no errors and no warnings`, () => {
      const r = validatePlugin(path.join(official, id))
      assert.deepEqual(r.findings, [])
      assert.equal(r.ok, true)
      assert.equal(r.manifest!.id, id)
    })
  }

  it('Reports every error in the manifest', () => {
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

  it('mcpServers accepts stdio and streamable HTTP entries (chosen the way Pi chooses the transport) and reports anything else', () => {
    const good = {
      stdio: { command: 'node', args: ['./mcp/server.js'] },
      typed_stdio: { type: 'stdio', command: 'node' },
      http: { url: 'http://127.0.0.1:3845/mcp', description: 'Local service' },
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
    assert.match(r.findings[0]!.message, /^"sse" uses SSE, which is not supported\. Use a streamable HTTP url instead\.$/)
    assert.match(r.findings[1]!.message, /^"ws" must have type stdio, http or streamable-http\.$/)
    assert.match(r.findings[2]!.message, /^"file_url" must be a stdio entry with a command or a streamable HTTP entry with an http or https url/)
  })

  it('A folder that is not a plugin, and bad JSON', () => {
    assert.match(validatePlugin(plugin('plain', { name: 'x', version: '1.0.0' })).findings[0]!.message, /so it is not a plugin/)
    assert.match(validatePlugin(plugin('broken', '{')).findings[0]!.message, /is not valid JSON/)
    assert.match(validatePlugin(path.join(tmp, 'absent')).findings[0]!.message, /is not a directory/)
    assert.match(validatePlugin(plugin('empty', { name: 'x', version: '1.0.0', loopNow: { id: 'x' } })).findings[0]!.message, /neither an Agent side/)
  })

  it('description and loopNow.displayName may be a string or a per-language object; the object needs at least one entry, language tags as keys and non-empty values', () => {
    const name = { 'zh-CN': '示例', 'en-US': 'Example' }  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
    const description = { 'zh-CN': '一个示例插件', en: 'An example plugin', 'zh-Hant-TW': '一個範例外掛' }  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
    const ok = validatePlugin(plugin('i18n-ok', { name: 'i18n-ok', version: '1.0.0', description, keywords: ['loop-now-plugin'], loopNow: { id: 'i18n-ok', displayName: name, mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
    assert.deepEqual(ok.findings, [])
    assert.deepEqual(ok.manifest!.displayName, name)
    assert.deepEqual(ok.manifest!.description, description)

    const bad: [string, unknown][] = [
      ['empty', {}],
      ['bad_key', { zh_CN: 'Example' }],
      ['long_key', { 'en-US-abcdefghi': 'x' }],
      ['blank_value', { 'zh-CN': '  ' }],
      ['number_value', { 'zh-CN': 1 }],
      ['number', 42],
      ['array', ['Example']],
      ['null', null],
      ['boolean', true],
    ]
    for (const [label, value] of bad) {
      const r = validatePlugin(plugin(`i18n-${label}`, { name: 'x', version: '1.0.0', description: value, keywords: ['loop-now-plugin'], loopNow: { id: 'x', displayName: value, mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
      assert.equal(r.ok, false, label)
      assert.deepEqual(
        r.findings.filter((f) => f.level === 'error').map((f) => [f.path, f.message]),
        [
          ['description', 'description must be a string, or an object with language tags (such as zh-CN, en-US) as keys and non-empty strings as values, with at least one entry.'],
          ['loopNow.displayName', 'loopNow.displayName must be a string, or an object with language tags (such as zh-CN, en-US) as keys and non-empty strings as values, with at least one entry.'],
        ],
        label,
      )
    }

    // Strings work as before: empty or missing only warns, and displayName falls back to the npm name.
    const plain = validatePlugin(plugin('i18n-plain', { name: 'plain-name', version: '1.0.0', description: '', keywords: ['loop-now-plugin'], loopNow: { id: 'plain', displayName: '', mcpServers: { s: { command: 'node' } } } }, { 'README.md': '' }))
    assert.equal(plain.ok, true)
    assert.equal(plain.manifest!.displayName, 'plain-name')
    assert.deepEqual(plain.findings.map((f) => f.path), ['description', 'loopNow.displayName'])
  })

  it('A Pi package with only a pi section gets its id from the npm name; warns about where dependencies go', () => {
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

  it('x-secret works at the top level of config and account fields, x-scope only at the top level of config; elsewhere they get a warning', () => {
    const secret = { type: 'string', 'x-secret': true }
    const r = validatePlugin(plugin('secret-places', {
      name: 'secret-places', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'],
      loopNow: {
        id: 'secret-places', displayName: 'X', platform: './p.ts',
        configSchema: { type: 'object', properties: { key: secret, team: { type: 'string', 'x-scope': 'project' } } },
        accounts: [{ kind: 'cloud', fields: { type: 'object', properties: {
          api_token: secret,
          nested: { type: 'object', properties: { inner: secret } },
          region: { type: 'string', 'x-scope': 'project' },
          port: { type: 'number', 'x-secret': true },
        } } }],
        actions: [{ id: 'go', label: 'Go', where: ['agent'], fields: { type: 'object', properties: { pin: secret } } }],
      },
    }, { 'p.ts': '', 'README.md': '' }))
    assert.deepEqual(r.findings.map((f) => [f.level, f.path]), [
      ['warning', 'loopNow.accounts[0].fields.properties.nested.properties.inner.x-secret'],
      ['warning', 'loopNow.accounts[0].fields.properties.region.x-scope'],
      ['error', 'loopNow.accounts[0].fields.properties.port.x-secret'],
      ['warning', 'loopNow.actions[0].fields.properties.pin.x-secret'],
    ])
  })

  it('A channel that manages external identities needs identities.manage', () => {
    const pkg = (permissions: string[]) => ({
      name: 'x', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'],
      loopNow: { id: 'x', displayName: 'X', platform: './p.ts', permissions, channels: [{ id: 'mail', label: 'Mail', manageIdentities: true }, { id: 'chat', label: 'Chat', manageIdentities: 'yes' }] },
    })
    const bad = validatePlugin(plugin('identity-bad', pkg([]), { 'p.ts': '', 'README.md': '' }))
    assert.deepEqual(bad.findings.map((f) => f.path), ['loopNow.channels[0].manageIdentities', 'loopNow.channels[1].manageIdentities'])
    const good = validatePlugin(plugin('identity-good', { ...pkg(['identities.manage']), loopNow: { ...pkg(['identities.manage']).loopNow, channels: [{ id: 'mail', label: 'Mail', manageIdentities: true }] } }, { 'p.ts': '', 'README.md': '' }))
    assert.deepEqual(good.findings, [])
    assert.deepEqual(good.manifest!.identityChannels, ['mail'])
  })

  it('labels and JSON Schema titles and descriptions may be LocalizedText (English first, with Chinese)', () => {
    const t = (english: string, chinese: string) => ({ 'en-US': english, 'zh-CN': chinese })
    const loopNow = {
      id: 'i18n-labels',
      displayName: t('Labels', '标签'),  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
      platform: './platform/index.ts',
      permissions: ['state'],
      configSchema: {
        type: 'object',
        properties: {
          url: { type: 'string', title: t('Callback URL', '回调地址'), description: t('Events are POSTed here', '事件会 POST 到这里') },  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
          brand: { type: 'string', title: 'Brand', oneOf: [{ const: 'feishu', title: t('Feishu', '飞书') }, { const: 'lark', title: 'Lark' }] },  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
          advanced: { type: 'object', title: t('Advanced', '高级'), properties: { region: { type: 'string', title: t('Region', '区域') } } },  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
        },
      },
      accounts: [{ kind: 'bot', label: t('Bot account', '机器人账号'), fields: { type: 'object', properties: { appId: { type: 'string', title: t('App ID', '应用 ID') } } } }],  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
      actions: [{ id: 'resend', label: t('Resend', '重发'), where: ['global'], fields: { type: 'object', properties: { note: { type: 'string', title: t('Note', '备注') } } } }],  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
      channels: [{ id: 'lark', label: t('Lark group', '飞书群') }],  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
    }
    const ok = validatePlugin(plugin('i18n-labels', { name: 'i18n-labels', version: '1.0.0', description: t('Labels in two languages', '两种语言的标签'), keywords: ['loop-now-plugin'], loopNow }, { 'README.md': '', 'platform/index.ts': 'export default () => {}\n' }))  // check-english: zh (LocalizedText: the Chinese entries are part of the data under test)
    assert.deepEqual(ok.findings, [])
    assert.equal(ok.ok, true)
  })

  it('a label or title that is neither a string nor a valid per-language object is reported at its path', () => {
    const rule = 'must be a string, or an object with language tags (such as zh-CN, en-US) as keys and non-empty strings as values, with at least one entry.'
    for (const [name, bad] of [['empty', {}], ['bad_key', { english: 'x' }], ['blank', { 'en-US': ' ' }], ['number', 42], ['boolean', true], ['array', ['x']], ['null', null]] as const) {
      const loopNow = {
        id: `bad-${name.replace('_', '-')}`,
        platform: './platform/index.ts',
        configSchema: { type: 'object', properties: { url: { type: 'string', title: bad, description: bad, oneOf: [{ const: 'a', title: bad }] } } },
        accounts: [{ kind: 'bot', label: bad, fields: { type: 'object', properties: {} } }],
        actions: [{ id: 'resend', label: bad, where: ['global'] }],
        channels: [{ id: 'lark', label: bad }],
      }
      const r = validatePlugin(plugin(`i18n-bad-${name}`, { name: 'x', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'], loopNow }, { 'README.md': '', 'platform/index.ts': 'export default () => {}\n' }))
      assert.equal(r.ok, false, name)
      const at = (path: string) => [path, `${path} ${rule}`]
      // A schema title or description that is not an object (a number, a boolean, an array, null) gets the same sentence here. The
      // server leaves those to the JSON Schema meta-schema, which rejects them at install time with its own wording; reporting them in
      // validate lets the plugin author see them first.
      assert.deepEqual(
        r.findings.filter((f) => f.level === 'error').map((f) => [f.path, f.message]),
        [
          at('loopNow.configSchema.properties.url.title'),
          at('loopNow.configSchema.properties.url.description'),
          at('loopNow.configSchema.properties.url.oneOf[0].title'),
          at('loopNow.accounts[0].label'),
          at('loopNow.actions[0].label'),
          at('loopNow.channels[0].label'),
        ],
        name,
      )
    }
  })

  it('titles and descriptions are checked in every keyword that holds subschemas, at the same paths as on the server', () => {
    const rule = 'must be a string, or an object with language tags (such as zh-CN, en-US) as keys and non-empty strings as values, with at least one entry.'
    const bad = { title: { english: 'x' } }
    const configSchema = {
      type: 'object',
      properties: { x: { type: 'object', properties: { y: { type: 'string', description: { english: 'x' } } } } },
      patternProperties: { '^p': bad },
      $defs: { d: bad },
      definitions: { e: bad },
      dependentSchemas: { x: bad },
      dependencies: { x: bad, y: ['x'] },
      items: bad,
      prefixItems: [bad],
      allOf: [bad],
      anyOf: [bad],
      oneOf: [bad],
      not: bad,
      if: bad,
      then: bad,
      else: bad,
      additionalProperties: bad,
      contains: bad,
      propertyNames: bad,
      unevaluatedItems: bad,
      unevaluatedProperties: bad,
      contentSchema: bad,
    }
    const loopNow = { id: 'deep-labels', platform: './platform/index.ts', configSchema, accounts: [{ kind: 'bot', label: 'Bot', fields: { type: 'object', properties: { token: { type: 'string', allOf: [bad] } } } }] }
    const r = validatePlugin(plugin('deep-labels', { name: 'x', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'], loopNow }, { 'README.md': '', 'platform/index.ts': 'export default () => {}\n' }))
    const at = (path: string) => [path, `${path} ${rule}`]
    const c = (path: string) => at(`loopNow.configSchema.${path}.title`)
    assert.deepEqual(r.findings.filter((f) => f.level === 'error').map((f) => [f.path, f.message]), [
      at('loopNow.configSchema.properties.x.properties.y.description'),
      ...['patternProperties.^p', '$defs.d', 'definitions.e', 'dependentSchemas.x', 'dependencies.x', 'prefixItems[0]', 'allOf[0]', 'anyOf[0]', 'oneOf[0]', 'items'].map(c),
      ...['not', 'if', 'then', 'else', 'additionalProperties', 'contains', 'propertyNames', 'unevaluatedItems', 'unevaluatedProperties', 'contentSchema'].map(c),
      at('loopNow.accounts[0].fields.properties.token.allOf[0].title'),
    ])
  })

  it('plain string labels work as before; a missing or empty action or channel label keeps the old error', () => {
    const base = { name: 'x', version: '1.0.0', description: 'x', keywords: ['loop-now-plugin'] }
    const ok = validatePlugin(plugin('labels-plain', { ...base, loopNow: { id: 'labels-plain', displayName: 'Plain', platform: './platform/index.ts', actions: [{ id: 'a', label: 'Resend', where: ['global'] }], channels: [{ id: 'c', label: 'Lark' }] } }, { 'README.md': '', 'platform/index.ts': 'export default () => {}\n' }))
    assert.deepEqual(ok.findings, [])
    const empty = validatePlugin(plugin('labels-empty', { ...base, loopNow: { id: 'labels-empty', platform: './platform/index.ts', actions: [{ id: 'a', label: '', where: ['global'] }], channels: [{ id: 'c' }] } }, { 'README.md': '', 'platform/index.ts': 'export default () => {}\n' }))
    assert.deepEqual(empty.findings.filter((f) => f.level === 'error').map((f) => f.message), ['Each entry in loopNow.actions must have a unique id, a label and where.', 'Each entry in loopNow.channels must have an id and a label.'])
  })

  it('Comparing platform versions', () => {
    assert.equal(versionAtLeast('2026.11', '2026.11'), true)
    assert.equal(versionAtLeast('2026.12.1', '2026.11'), true)
    assert.equal(versionAtLeast('2026.10', '2026.11'), false)
    assert.equal(versionAtLeast('dev', '2030.01'), true)
  })
})
