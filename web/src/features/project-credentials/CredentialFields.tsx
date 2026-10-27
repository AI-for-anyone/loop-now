import { useId, useRef } from 'react'
import type { Schemas } from '@/api/client'
import { FieldError } from '@/components/ErrorAlert'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'
import { Textarea } from '@/components/ui/textarea'

// 沿用当前正式模板定义；这里仅渲染输入，不能接收已保存秘密或执行请求。
export function CredentialFields({ fields, values, errors = {}, disabled, onChange }: {
  fields: Schemas['CliTemplate']['fields']
  values: Record<string, string>
  errors?: Record<string, string>
  disabled?: boolean
  onChange: (name: string, value: string) => void
}) {
  const prefix = useId()
  return <>{fields.map(field => {
    const id = `${prefix}-${field.name}`
    return <div key={field.name} className="grid min-w-0 gap-1.5">
      <Label htmlFor={id}>{field.label}{field.required ? ' *' : ''}</Label>
      {field.type === 'enum'
        ? <select id={id} className={selectClass} value={values[field.name] ?? ''} disabled={disabled} aria-invalid={!!errors[field.name]} onChange={e => onChange(field.name, e.target.value)}>
          {(field.options ?? []).map(option => <option key={option} value={option}>{option}</option>)}
        </select>
        : field.multiline ? <MultilineField id={id} secret={field.type === 'secret'} value={values[field.name] ?? ''} disabled={disabled} invalid={!!errors[field.name]} onChange={value => onChange(field.name, value)} />
        : <Input id={id} type={field.type === 'secret' ? 'password' : 'text'} autoComplete={field.type === 'secret' ? 'new-password' : 'off'} value={values[field.name] ?? ''} disabled={disabled} aria-invalid={!!errors[field.name]} onChange={e => onChange(field.name, e.target.value)} />}
      <FieldError message={errors[field.name]} />
    </div>
  })}</>
}

// Textareas have no password type. Keep glyphs transparent in every browser,
// and render only a same-sized masked overlay (never the private key text).
function MultilineField({ id, secret, value, disabled, invalid, onChange }: { id: string; secret: boolean; value: string; disabled?: boolean; invalid: boolean; onChange: (value: string) => void }) {
  const mask = useRef<HTMLDivElement>(null)
  return <div className="relative min-w-0">
    <Textarea id={id} rows={5} spellCheck={false} autoComplete={secret ? 'new-password' : 'off'} value={value} disabled={disabled} aria-invalid={invalid} className={`resize-y px-3 py-3 font-mono text-sm ${secret ? 'text-transparent caret-ink selection:text-transparent' : ''}`} onChange={event => onChange(event.target.value)} onScroll={event => { if (mask.current) { mask.current.scrollTop = event.currentTarget.scrollTop; mask.current.scrollLeft = event.currentTarget.scrollLeft } }} />
    {secret && <div ref={mask} aria-hidden="true" className="pointer-events-none absolute inset-0 overflow-hidden rounded-input px-3 py-3 font-mono text-sm leading-[1.7] whitespace-pre-wrap break-words text-ink">{value.replace(/[^\n\r]/g, '•')}</div>}
  </div>
}
