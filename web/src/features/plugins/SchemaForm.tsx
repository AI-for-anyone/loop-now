import { useId, useState, type FormEvent, type ReactNode } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import { cn } from '@/lib/utils'
import {
  describeValue,
  fieldsOf,
  initialDraft,
  isDirty,
  serverFieldErrors,
  toPayload,
  validate,
  type Draft,
  type Field,
  type FormMode,
  type SecretDraft,
} from './schema-form'

const text = t.plugins.form

type Config = Schemas['PluginConfig']

const hintClass = 'text-[12.5px] leading-snug text-ink-500'

// Inputs generated from the fields. In project mode every field may be left empty to keep using the global value, which is shown below
// it; booleans become a choice of three (use global value, yes, no).
export function SchemaFields({
  fields,
  draft,
  onChange,
  errors,
  disabled,
  mode = 'global',
  secretsSet = [],
  globalValues,
  globalSecretsSet = [],
}: {
  fields: Field[]
  draft: Draft
  onChange: (draft: Draft) => void
  errors: Record<string, string>
  disabled?: boolean
  mode?: FormMode
  secretsSet?: string[]
  globalValues?: Record<string, unknown>
  globalSecretsSet?: string[]
}) {
  const id = useId()
  const setValue = (name: string, value: string) => onChange({ ...draft, values: { ...draft.values, [name]: value } })
  const setSecret = (name: string, secret: SecretDraft) => onChange({ ...draft, secrets: { ...draft.secrets, [name]: secret } })
  return (
    <div className="grid min-w-0 gap-4">
      {fields.map((field) => {
        const inputId = `${id}-${field.name}`
        const hintId = `${inputId}-hint`
        const error = errors[field.name]
        const global =
          mode === 'project'
            ? field.secret
              ? globalSecretsSet.includes(field.name)
                ? text.secretSet
                : null
              : describeValue(field, globalValues?.[field.name])
            : null
        const hints = [
          field.required ? text.required : null,
          field.secret ? `${text.secret} · ${text.secretHint}` : null,
          field.description,
          mode === 'global' && field.defaultValue !== undefined ? text.defaultValue(describeValue(field, field.defaultValue) ?? '') : null,
          mode === 'project' ? (global === null ? t.plugins.project.globalUnset : t.plugins.project.globalValue(global)) : null,
        ].filter(Boolean)
        const common = { id: inputId, disabled, 'aria-invalid': error ? true : undefined, 'aria-describedby': hintId }
        return (
          <div key={field.name} className="grid min-w-0 gap-1.5">
            {field.kind === 'boolean' && mode === 'global' ? (
              <label className="inline-flex items-center gap-2 text-[14px] font-medium">
                <input
                  type="checkbox"
                  {...common}
                  checked={draft.values[field.name] === 'true'}
                  onChange={(event) => setValue(field.name, event.target.checked ? 'true' : 'false')}
                  className="size-4 accent-ink"
                />
                {field.label}
              </label>
            ) : (
              <Label htmlFor={inputId}>{field.label}</Label>
            )}
            {field.secret ? (
              <SecretInput
                inputId={inputId}
                hintId={hintId}
                secret={draft.secrets[field.name] ?? { mode: 'set', value: '' }}
                wasSet={secretsSet.includes(field.name)}
                clearable={!field.required}
                disabled={disabled}
                invalid={Boolean(error)}
                label={field.label}
                onChange={(secret) => setSecret(field.name, secret)}
              />
            ) : field.kind === 'unsupported' ? (
              <p id={inputId} className="rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13px] text-ink-700">
                {text.unsupported}
              </p>
            ) : field.kind === 'enum' || (field.kind === 'boolean' && mode === 'project') ? (
              <select {...common} value={draft.values[field.name] ?? ''} onChange={(event) => setValue(field.name, event.target.value)} className={selectClass}>
                <option value="" disabled={mode === 'global' && field.required}>
                  {mode === 'project' ? t.plugins.config.useGlobal : text.choose}
                </option>
                {field.kind === 'boolean' ? (
                  <>
                    <option value="true">{text.yes}</option>
                    <option value="false">{text.no}</option>
                  </>
                ) : (
                  field.options.map((option, index) => (
                    <option key={index} value={String(index)}>
                      {option.label}
                    </option>
                  ))
                )}
              </select>
            ) : field.kind === 'boolean' ? null : (
              <Input
                {...common}
                type={field.kind === 'uri' ? 'url' : 'text'}
                inputMode={field.kind === 'number' ? 'decimal' : field.kind === 'integer' ? 'numeric' : undefined}
                value={draft.values[field.name] ?? ''}
                placeholder={mode === 'project' ? (global ?? undefined) : undefined}
                onChange={(event) => setValue(field.name, event.target.value)}
              />
            )}
            <p id={hintId} className={hintClass}>
              {hints.join(' · ')}
            </p>
            <FieldError message={error} />
          </div>
        )
      })}
    </div>
  )
}

// A secret field: write-only, never echoed back. Once set, it shows “已设置 · 更换” (Set · Replace), and the input appears only after
// clicking Replace; optional ones can also be cleared.
function SecretInput({
  inputId,
  hintId,
  secret,
  wasSet,
  clearable,
  disabled,
  invalid,
  label,
  onChange,
}: {
  inputId: string
  hintId: string
  secret: SecretDraft
  wasSet: boolean
  clearable: boolean
  disabled?: boolean
  invalid: boolean
  label: string
  onChange: (secret: SecretDraft) => void
}) {
  const action = 'shrink-0 cursor-pointer text-[13px] font-bold text-riso-blue underline outline-none focus-visible:shadow-focus disabled:cursor-default disabled:opacity-40'
  if (wasSet && secret.mode !== 'set')
    return (
      <div id={inputId} aria-describedby={hintId} className={cn('flex h-11 min-w-0 items-center gap-3 rounded-input border border-hairline-strong bg-surface-control px-3.5', secret.mode === 'clear' && 'bg-tint-yellow')}>
        <span className="min-w-0 flex-1 truncate text-[14px] text-ink-700">
          {secret.mode === 'clear' ? text.secretCleared : <><span aria-hidden="true" className="tracking-[3px]">{'••••••••'}</span> {text.secretSet}</>}
        </span>
        {secret.mode === 'clear' ? (
          <button type="button" className={action} disabled={disabled} onClick={() => onChange({ mode: 'keep', value: '' })}>
            {text.secretKeep}
          </button>
        ) : (
          <>
            <button type="button" className={action} disabled={disabled} aria-label={`${text.secretReplace} ${label}`} onClick={() => onChange({ mode: 'set', value: '' })}>
              {text.secretReplace}
            </button>
            {clearable && (
              <button type="button" className={action} disabled={disabled} aria-label={`${text.secretClear} ${label}`} onClick={() => onChange({ mode: 'clear', value: '' })}>
                {text.secretClear}
              </button>
            )}
          </>
        )}
      </div>
    )
  return (
    <div className="flex min-w-0 items-center gap-3">
      <Input
        id={inputId}
        type="password"
        autoComplete="new-password"
        aria-describedby={hintId}
        aria-invalid={invalid || undefined}
        disabled={disabled}
        value={secret.value}
        onChange={(event) => onChange({ mode: 'set', value: event.target.value })}
      />
      {wasSet && (
        <button type="button" className={action} disabled={disabled} onClick={() => onChange({ mode: 'keep', value: '' })}>
          {text.secretKeep}
        </button>
      )}
    </div>
  )
}

export type ConfigPayload = { values: Record<string, unknown>; secrets?: Record<string, string | null>; expectedRevision: number }

const isConflict = (error: unknown) => error instanceof ApiError && error.status === 412

// The config form's save flow: after local validation, save against expectedRevision; on 412 (changed elsewhere), explain and let the
// person choose between loading the latest and overwriting with their own changes;
// 422 field errors are marked on the matching fields. When the server's config moves to a new revision and nothing was changed here,
// switch to the new revision directly.
export function ConfigForm({
  formId,
  schema,
  config,
  mode = 'global',
  globalConfig,
  onSave,
  reload,
  disabled,
  extraDirty = false,
  footer,
  saveLabel = text.save,
  emptyText,
}: {
  formId: string
  schema: unknown
  config: Config
  mode?: FormMode
  globalConfig?: Config
  onSave: (payload: ConfigPayload) => Promise<Config>
  reload: () => Promise<Config | undefined>
  disabled?: boolean
  extraDirty?: boolean
  footer?: ReactNode
  saveLabel?: string
  emptyText?: string
}) {
  const fields = fieldsOf(schema, mode)
  const [base, setBase] = useState(config)
  const [initial, setInitial] = useState(() => initialDraft(fields, config, mode))
  const [draft, setDraft] = useState(initial)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [error, setError] = useState<unknown>(null)
  const [conflict, setConflict] = useState(false)
  const [pending, setPending] = useState(false)
  const [saved, setSaved] = useState(false)
  const dirty = isDirty(initial, draft) || extraDirty
  // When the global config has never been saved, it can be saved as is, storing the defaults from the manifest.
  const firstSave = mode === 'global' && base.revision === 0
  useDirtyForm(formId, dirty)

  const adopt = (next: Config) => {
    const fresh = initialDraft(fields, next, mode)
    setBase(next)
    setInitial(fresh)
    setDraft(fresh)
    setErrors({})
  }
  // The server has a newer revision (saved elsewhere, or saved by the enable switch) and nothing was changed here: switch to the new revision.
  if (config.revision > base.revision && !dirty && !pending) adopt(config)

  // against is the server config this save is checked against: normally the current revision; when overwriting, the latest revision just
  // loaded.
  const submit = async (against: Config) => {
    const local = validate(fields, draft, against.secretsSet)
    setErrors(local)
    if (Object.keys(local).length) return
    setPending(true)
    setError(null)
    setSaved(false)
    try {
      const next = await onSave({ ...toPayload(fields, draft, against.values), expectedRevision: against.revision })
      setConflict(false)
      adopt(next)
      setSaved(true)
    } catch (cause) {
      if (isConflict(cause)) setConflict(true)
      else {
        setError(cause)
        if (cause instanceof ApiError) setErrors(serverFieldErrors(fields, cause.fields))
      }
    } finally {
      setPending(false)
    }
  }
  const onSubmit = (event: FormEvent) => {
    event.preventDefault()
    if (!pending && (dirty || firstSave)) void submit(base)
  }
  const resolve = async (keepMine: boolean) => {
    setPending(true)
    try {
      const latest = await reload()
      if (!latest) return
      if (keepMine) {
        setBase(latest)
        await submit(latest)
      } else {
        setConflict(false)
        adopt(latest)
      }
    } catch (cause) {
      setError(cause)
    } finally {
      setPending(false)
    }
  }

  const busy = pending || disabled
  return (
    <form className="grid min-w-0 gap-4" onSubmit={onSubmit} noValidate>
      {fields.length === 0 ? (
        <p className="text-[14px] text-ink-700">{emptyText ?? t.plugins.config.none}</p>
      ) : (
        <SchemaFields
          fields={fields}
          draft={draft}
          onChange={(next) => {
            setDraft(next)
            setSaved(false)
          }}
          errors={errors}
          disabled={busy}
          mode={mode}
          secretsSet={base.secretsSet}
          globalValues={globalConfig?.values}
          globalSecretsSet={globalConfig?.secretsSet}
        />
      )}
      {conflict && (
        <div role="group" aria-label={text.conflictTitle} className="grid min-w-0 gap-2.5">
          <Alert variant="warning" role="status">
            <AlertDescription>
              <p className="font-semibold">{text.conflictTitle}</p>
              <p>{text.conflictBody}</p>
            </AlertDescription>
          </Alert>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="secondary" disabled={pending} onClick={() => void resolve(false)}>
              {text.reload}
            </Button>
            <Button type="button" disabled={pending} onClick={() => void resolve(true)}>
              {text.overwrite}
            </Button>
          </div>
        </div>
      )}
      <ErrorAlert error={error} />
      <div className="flex flex-wrap items-center justify-end gap-3">
        {footer && <div className="mr-auto text-[13px] text-ink-700">{footer}</div>}
        {dirty && (
          <p role="status" className="rounded-full border border-hairline bg-tint-yellow px-3 py-0.5 text-[13px] font-semibold">
            {Object.keys(errors).length ? text.fix : text.unsaved}
          </p>
        )}
        {saved && !dirty && (
          <p role="status" className="text-[13px] font-medium text-green-text">
            {text.saved}
          </p>
        )}
        {(fields.length > 0 || extraDirty) && (
          <Button type="submit" disabled={busy || conflict || (!dirty && !firstSave)}>
            {pending ? text.saving : saveLabel}
          </Button>
        )}
      </div>
    </form>
  )
}
