import { t } from '@/i18n'
import { useId, useState, type FormEvent } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { WarningList } from '@/features/agent-config/CredentialStatus'
import { CredentialConflict } from './CredentialConflict'
import { CredentialFields } from './CredentialFields'
import { useCredentialDraft } from './useCredentialDraft'
import { fieldMessages, unmatchedFieldError } from '@/lib/errors'

// 本地表单值，不是HTTP载荷；接线时调用方按正式生成的credential oneOf构造请求。
export type CredentialInput = { name: string; key: string; fields: Record<string, string> }
export function CredentialEditor({ id, name = '', mode, template, revision, summary, readLatest, onSave, onClose }: {
  id: string
  name?: string
  mode: 'create-key' | 'create-cli' | 'replace-key' | 'replace-cli' | 'rename'
  template?: Schemas['CliTemplate']
  revision: number
  summary: string
  readLatest: () => Promise<{ revision: number; summary: string; deleted?: boolean }>
  onSave: (input: CredentialInput, revision: number) => Promise<void>
  onClose: () => void
}) {
  const prefix = useId()
  const [initial] = useState<CredentialInput>(() => ({ name, key: '', fields: Object.fromEntries((template?.fields ?? []).map(field => [field.name, field.default ?? (field.type === 'enum' ? field.options?.[0] ?? '' : '')])) }))
  const form = useCredentialDraft({ id, initial, revision, summarize: summary, readLatest, save: onSave, onSaved: onClose })
  const [errors, setErrors] = useState<Record<string, string>>({})
  const cli = mode.endsWith('cli'), key = mode.endsWith('key'), editingName = mode.startsWith('create') || mode === 'rename'
  const missingTemplate = cli && !template
  const nameError = errors.name ?? fieldMessages(form.error, field => field === 'name')
  const keyError = errors.key ?? fieldMessages(form.error, field => field === 'credential.key')
  const fieldErrors = Object.fromEntries((template?.fields ?? []).map(field => [field.name, errors[field.name] ?? fieldMessages(form.error, path => path === `credential.fields.${field.name}`) ?? '']))
  const inline = (field: string) => (editingName && field === 'name') || (key && field === 'credential.key') || (cli && !!template?.fields.some(item => field === `credential.fields.${item.name}`))
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const next: Record<string, string> = {}
    if (editingName && !form.draft.name.trim()) next.name = t.credentials.editor.nameRequired
    if (key && !form.draft.key.trim()) next.key = t.credentials.editor.keyRequired
    if (cli) for (const field of template?.fields ?? []) if (field.required && !form.draft.fields[field.name]) next[field.name] = t.credentials.editor.fieldRequired
    setErrors(next)
    if (!missingTemplate && Object.keys(next).length === 0) void form.submit()
  }
  return <form className="grid min-w-0 gap-4" onSubmit={submit} autoComplete="off" noValidate>
    {editingName && <div className="grid gap-1.5"><Label htmlFor={`${prefix}-name`}>{t.credentials.editor.nameLabel}</Label><Input id={`${prefix}-name`} maxLength={200} value={form.draft.name} disabled={form.pending} aria-invalid={!!nameError} onChange={e => form.setDraft(v => ({ ...v, name: e.target.value }))} /><FieldError message={nameError} /></div>}
    {key && <div className="grid gap-1.5"><Label htmlFor={`${prefix}-key`}>API Key</Label><Input id={`${prefix}-key`} type="password" autoComplete="new-password" value={form.draft.key} disabled={form.pending} aria-invalid={!!keyError} onChange={e => form.setDraft(v => ({ ...v, key: e.target.value }))} /><FieldError message={keyError} /></div>}
    {cli && template && <><p className="text-[13px]">{t.credentials.editor.templatePrefix}{template.name} · v{template.version}</p><WarningList warnings={template.warnings} /><CredentialFields fields={template.fields} values={form.draft.fields} errors={fieldErrors} disabled={form.pending} onChange={(field, value) => form.setDraft(v => ({ ...v, fields: { ...v.fields, [field]: value } }))} /></>}
    {missingTemplate && <p role="alert">{t.credentials.editor.templateMissing}</p>}
    {mode !== 'rename' && <p className="text-[13px] text-ink-700">{t.credentials.editor.saveNote}</p>}
    {form.conflict && <CredentialConflict base={form.base.summary} latest={form.review} reading={form.reading} error={form.readError} onRefresh={() => void form.refresh()} onAdopt={form.adopt} />}
    {!form.conflict && <ErrorAlert error={unmatchedFieldError(form.error, inline)} />}
    {form.error instanceof ApiError && form.error.status === 0 && <p role="status" className="text-[13px] font-bold">{t.credentials.editor.saveUnknown}</p>}
    <div className="flex flex-wrap justify-end gap-2"><Button type="button" variant="secondary" disabled={form.pending} onClick={onClose}>{t.credentials.editor.cancel}</Button><Button type="submit" disabled={form.pending || form.conflict || missingTemplate}>{form.pending ? t.credentials.editor.saving : t.credentials.editor.save}</Button></div>
  </form>
}
