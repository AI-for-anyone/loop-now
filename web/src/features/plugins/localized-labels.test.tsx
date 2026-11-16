// @vitest-environment-options {"url": "http://localhost:3000/?lang=en-US"}
import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { CredentialFields } from '@/features/project-credentials/CredentialFields'
import { makeComponents, makeManifest } from '@/test/plugins'
import { componentChips } from './manifest'
import { ManifestSummary } from './ManifestSummary'
import { fieldsOf } from './schema-form'
import { SchemaFields } from './SchemaForm'

// Labels written by plugin and template authors may be LocalizedText (English first, with Chinese). The English UI shows the
// en-US text; a plain string is shown as is in every language. The Chinese UI is covered in localized-labels.zh.test.tsx.
const en = (english: string, chinese: string) => ({ 'en-US': english, 'zh-CN': chinese })

const schema = {
  type: 'object',
  required: ['url'],
  properties: {
    url: { type: 'string', format: 'uri', title: en('Callback URL', '回调地址'), description: en('Events are POSTed here', '事件会 POST 到这里') },
    retries: { type: 'integer', title: 'Retries' },
    brand: { type: 'string', title: en('Brand', '品牌'), oneOf: [{ const: 'feishu', title: en('Feishu', '飞书') }, { const: 'lark', title: 'Lark' }] },
    nested: { type: 'object', title: en('Advanced', '高级'), properties: { inner: { type: 'string', title: en('Inner', '内部') } } },
  },
}

describe('LocalizedText labels in the English UI', () => {
  it('fieldsOf resolves title, description and oneOf option titles; a plain string stays as is', () => {
    const fields = fieldsOf(schema)
    expect(fields.map((field) => field.label)).toEqual(['Callback URL', 'Retries', 'Brand', 'Advanced'])
    expect(fields[0].description).toBe('Events are POSTed here')
    expect(fields[2].options.map((option) => option.label)).toEqual(['Feishu', 'Lark'])
  })

  it('a title that is neither a string nor a per-language object falls back to the field name', () => {
    expect(fieldsOf({ type: 'object', properties: { odd: { type: 'string', title: 42 } } })[0].label).toBe('odd')
  })

  it('the config form shows the English labels and description', () => {
    const fields = fieldsOf(schema)
    render(<SchemaFields fields={fields} draft={{ values: {}, secrets: {} }} onChange={vi.fn()} errors={{}} />)
    expect(screen.getByLabelText(/^Callback URL/)).toBeInTheDocument()
    expect(screen.getByText(/Events are POSTed here/)).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Feishu' })).toBeInTheDocument()
  })

  it('CLI template field labels in the credential form', () => {
    const fields: Schemas['CliTemplate']['fields'] = [
      { name: 'host', label: en('Host', '主机'), type: 'string', required: true },
      { name: 'token', label: 'Token', type: 'secret', required: false },
    ]
    render(<CredentialFields fields={fields} values={{ host: '', token: '' }} onChange={vi.fn()} />)
    expect(screen.getByLabelText(/^Host/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^Token/)).toBeInTheDocument()
  })

  it('channel, account kind and action labels in the manifest chips and summary', () => {
    const manifest = makeManifest({
      configSchema: schema as never,
      accounts: [{ kind: 'bot', label: en('Bot account', '机器人账号'), fields: { type: 'object', properties: { appId: { type: 'string', title: en('App ID', '应用 ID') } } } as never, multiple: false }],
      components: makeComponents({ channels: [{ id: 'lark', label: en('Lark group', '飞书群'), manageIdentities: true }], actions: [{ id: 'resend', label: en('Resend', '重发'), fields: {} as never, where: ['global'] }] }),
    })
    expect(componentChips(manifest)).toEqual(expect.arrayContaining([expect.stringContaining('Lark group'), expect.stringContaining('Bot account')]))
    render(<ManifestSummary manifest={manifest} />)
    for (const text of ['Lark group', 'Bot account', 'App ID', 'Resend', 'Callback URL']) expect(document.body).toHaveTextContent(text)
    expect(document.body).not.toHaveTextContent(/飞书群|机器人账号|应用 ID|重发|回调地址/)
  })
})
