import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { CredentialFields } from '@/features/project-credentials/CredentialFields'
import { fieldsOf } from '@/features/plugins/schema-form'

// The Chinese UI picks the zh-CN text of a LocalizedText label; a plain string (counted as English) is shown as is.
const en = (english: string, chinese: string) => ({ 'en-US': english, 'zh-CN': chinese })

describe('LocalizedText labels in the Chinese UI', () => {
  it('fieldsOf takes the zh-CN text; a plain string stays as is', () => {
    const fields = fieldsOf({
      type: 'object',
      properties: {
        url: { type: 'string', title: en('Callback URL', '回调地址'), description: en('Events are POSTed here', '事件会 POST 到这里') },
        retries: { type: 'integer', title: 'Retries' },
        brand: { type: 'string', oneOf: [{ const: 'feishu', title: en('Feishu', '飞书') }, { const: 'lark', title: 'Lark' }] },
        onlyEnglish: { type: 'string', title: { 'en-US': 'Region' } },
      },
    })
    expect(fields.map((field) => field.label)).toEqual(['回调地址', 'Retries', 'brand', 'Region'])
    expect(fields[0].description).toBe('事件会 POST 到这里')
    expect(fields[2].options.map((option) => option.label)).toEqual(['飞书', 'Lark'])
  })

  it('CLI template field labels in the credential form', () => {
    const fields: Schemas['CliTemplate']['fields'] = [
      { name: 'host', label: en('Host', '主机'), type: 'string', required: true },
      { name: 'token', label: 'Token', type: 'secret', required: false },
    ]
    render(<CredentialFields fields={fields} values={{ host: '', token: '' }} onChange={vi.fn()} />)
    expect(screen.getByLabelText(/^主机/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^Token/)).toBeInTheDocument()
  })
})
