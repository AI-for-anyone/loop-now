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

// 按字段生成的输入项。project 模式下每一项都可以留空沿用全局值，下面写出全局值；布尔值改成三选一（用全局值、是、否）。
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
              <label className="inline-flex items-center gap-2 text-[14px] font-bold">
                <input
                  type="checkbox"
                  {...common}
                  checked={draft.values[field.name] === 'true'}
                  onChange={(event) => setValue(field.name, event.target.checked ? 'true' : 'false')}
                  className="size-4 accent-riso-blue"
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
              <p id={inputId} className="rounded-input bg-ink/[0.04] px-3 py-2 text-[13px] text-ink-700">
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

// secret 字段：只写不回显。设置过时显示“已设置 · 更换”，点更换才出现输入框；可选的还能清除。
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
      <div id={inputId} aria-describedby={hintId} className={cn('flex h-11 min-w-0 items-center gap-3 rounded-input bg-white px-3.5 shadow-[inset_0_0_0_2px_rgb(24_38_94/.18)]', secret.mode === 'clear' && 'bg-tint-yellow')}>
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

// 配置表单的保存流程：本地校验后按 expectedRevision 保存；412（别处改过）时说明并让人选载入最新或用自己的修改覆盖；
// 422 的字段错误标到对应的项。服务端的配置换了版本、而这里没有改动时直接换成新版本。
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
  // 全局配置从未保存过时可以直接保存，存下清单里的默认值。
  const firstSave = mode === 'global' && base.revision === 0
  useDirtyForm(formId, dirty)

  const adopt = (next: Config) => {
    const fresh = initialDraft(fields, next, mode)
    setBase(next)
    setInitial(fresh)
    setDraft(fresh)
    setErrors({})
  }
  // 服务端有了更新的版本（别处保存、启用开关保存）且这里没有改动：换成新版本。
  if (config.revision > base.revision && !dirty && !pending) adopt(config)

  // against 是这次保存所对照的服务端配置：平常是当前版本，覆盖时是刚载入的最新版本。
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
              <p className="font-black">{text.conflictTitle}</p>
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
          <p role="status" className="rounded-full bg-tint-yellow px-3 py-0.5 text-[13px] font-black">
            {Object.keys(errors).length ? text.fix : text.unsaved}
          </p>
        )}
        {saved && !dirty && (
          <p role="status" className="text-[13px] font-bold text-green-text">
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
