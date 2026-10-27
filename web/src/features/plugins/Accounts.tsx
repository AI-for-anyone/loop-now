import { useId, useState, type FormEvent } from 'react'
import { ApiError, type Schemas } from '@/api/client'
import { useCreatePluginAccount, useDeletePluginAccount, usePluginAccounts, useUpdatePluginAccount } from '@/api/plugins'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { pluginFormText, pluginText } from '@/i18n/plugins'
import { cn } from '@/lib/utils'
import { SchemaFields } from './SchemaForm'
import { fieldsOf, initialDraft, isDirty, serverFieldErrors, toPayload, validate } from './schema-form'
import { Quiet } from './parts'

type Account = Schemas['PluginAccount']
type Kind = Schemas['PluginAccountKind']
const text = pluginText.accounts

// 账号一行的摘要：第一个非空的普通字段（截断），secret 一律不显示。
function preview(account: Account): string | null {
  const value = Object.values(account.values as Record<string, unknown>).find((v) => typeof v === 'string' && v !== '') as string | undefined
  return value ? (value.length > 12 ? `${value.slice(0, 10)}…` : value) : null
}

// 添加或修改一个账号：名称加上这个种类的字段（按 JSON Schema 生成，密钥只写）。
// 修改按版本保存；别处改过（412）时说明并关上表单，列表随之重新读取。
function AccountForm({ projectId, pluginId, kind, account, onDone }: { projectId: string; pluginId: string; kind: Kind; account?: Account; onDone: () => void }) {
  const fields = fieldsOf(kind.fields)
  const id = useId()
  const create = useCreatePluginAccount(projectId, pluginId)
  const update = useUpdatePluginAccount(projectId, pluginId, account?.id ?? '')
  const saved = { values: (account?.values ?? {}) as Record<string, unknown>, secretsSet: account?.secretsSet ?? [], revision: account?.revision ?? 0 }
  const [initial] = useState(() => initialDraft(fields, saved))
  const [draft, setDraft] = useState(initial)
  const [name, setName] = useState(account?.name ?? '')
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [nameError, setNameError] = useState<string | undefined>()
  const mutation = account ? update : create
  const dirty = isDirty(initial, draft) || name !== (account?.name ?? '')
  useDirtyForm(`plugin-account:${pluginId}:${account?.id ?? `new-${kind.kind}`}`, dirty)
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const local = validate(fields, draft, saved.secretsSet)
    const missingName = name.trim() === '' ? pluginFormText.required : undefined
    setErrors(local)
    setNameError(missingName)
    if (Object.keys(local).length || missingName) return
    const { values, secrets } = toPayload(fields, draft, saved.values)
    const onError = (error: ApiError) => setErrors(serverFieldErrors(fields, error.fields))
    if (account) update.mutate({ name: name.trim(), values: values as never, ...(secrets ? { secrets } : {}), expectedRevision: account.revision }, { onSuccess: onDone, onError })
    else {
      // 新账号只能设值，不会有“清除”。
      const set = Object.fromEntries(Object.entries(secrets ?? {}).filter((entry): entry is [string, string] => entry[1] !== null))
      create.mutate({ kind: kind.kind, name: name.trim(), values: values as never, ...(Object.keys(set).length ? { secrets: set } : {}) }, { onSuccess: onDone, onError })
    }
  }
  const conflict = mutation.error instanceof ApiError && mutation.error.status === 412
  return (
    <form className="grid min-w-0 gap-3.5 rounded-input bg-tint-blue/40 p-3.5" onSubmit={submit} noValidate>
      <div className="grid gap-1.5">
        <Label htmlFor={`${id}-name`}>{text.name}</Label>
        <Input id={`${id}-name`} value={name} aria-invalid={nameError ? true : undefined} onChange={(event) => setName(event.target.value)} autoComplete="off" />
        <FieldError message={nameError} />
      </div>
      <SchemaFields fields={fields} draft={draft} onChange={setDraft} errors={errors} disabled={mutation.isPending} secretsSet={saved.secretsSet} />
      {conflict ? <p role="status" className="text-[13px] font-bold">{pluginFormText.conflictBody}</p> : <ErrorAlert error={mutation.error} />}
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" size="sm" variant="secondary" disabled={mutation.isPending} onClick={onDone}>
          {text.cancel}
        </Button>
        {!conflict && (
          <Button type="submit" size="sm" disabled={mutation.isPending || (account !== undefined && !dirty)}>
            {mutation.isPending ? pluginFormText.saving : text.save}
          </Button>
        )}
      </div>
    </form>
  )
}

// 项目里一个插件的账号：按清单声明的种类分组，每组列出账号，可添加、修改、删除。不支持多账号的种类有一个后不能再加。
export function AccountsManager({ projectId, pluginId, kinds }: { projectId: string; pluginId: string; kinds: Kind[] }) {
  const accounts = usePluginAccounts(projectId, pluginId, kinds.length > 0)
  const remove = useDeletePluginAccount(projectId, pluginId)
  const [editing, setEditing] = useState<string | null>(null)
  const [removing, setRemoving] = useState<Account | null>(null)
  if (!kinds.length) return <Quiet>{text.none}</Quiet>
  const list = accounts.data?.accounts ?? []
  return (
    <div className="grid min-w-0 gap-4">
      <ErrorAlert error={accounts.error} />
      {kinds.map((kind) => {
        const mine = list.filter((account) => account.kind === kind.kind)
        const full = !kind.multiple && mine.length > 0
        return (
          <section key={kind.kind} aria-label={text.title(kind.label)} className="grid min-w-0 gap-2">
            <h3 className="text-[13px] font-black text-ink-700">
              {text.title(kind.label)}
              {!kind.multiple && <span className="ml-2 font-bold text-ink-500">{text.single}</span>}
            </h3>
            {accounts.isSuccess && mine.length === 0 && editing !== `new:${kind.kind}` && <Quiet>{text.empty}</Quiet>}
            <ul className="grid min-w-0 gap-1.5">
              {mine.map((account) =>
                editing === account.id ? (
                  <li key={account.id}>
                    <AccountForm projectId={projectId} pluginId={pluginId} kind={kind} account={account} onDone={() => setEditing(null)} />
                  </li>
                ) : (
                  <li key={account.id} className="flex min-w-0 items-center gap-3 rounded-input bg-ink/[0.04] px-3 py-2 text-[13px]">
                    <span className="min-w-0 truncate font-black">{account.name}</span>
                    {preview(account) && <span className="min-w-0 truncate font-mono text-[12px] text-ink-500">{preview(account)}</span>}
                    <span className="ml-auto flex shrink-0 gap-3">
                      <button type="button" className="cursor-pointer font-bold text-riso-blue underline outline-none focus-visible:shadow-focus" aria-label={`${text.edit} ${account.name}`} onClick={() => setEditing(account.id)}>
                        {text.edit}
                      </button>
                      <button type="button" className="cursor-pointer font-bold text-red-text underline outline-none focus-visible:shadow-focus" aria-label={`${text.remove} ${account.name}`} onClick={() => setRemoving(account)}>
                        {text.remove}
                      </button>
                    </span>
                  </li>
                ),
              )}
            </ul>
            {editing === `new:${kind.kind}` ? (
              <AccountForm projectId={projectId} pluginId={pluginId} kind={kind} onDone={() => setEditing(null)} />
            ) : (
              !full && (
                <Button size="xs" variant="secondary" className={cn('justify-self-start')} disabled={!accounts.isSuccess} onClick={() => setEditing(`new:${kind.kind}`)}>
                  {text.add}
                </Button>
              )
            )}
          </section>
        )
      })}
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={text.removeTitle(removing?.name ?? '')}
        body={text.removeBody}
        confirmLabel={text.remove}
        destructive
        onConfirm={() => (removing ? remove.mutateAsync(removing.id) : undefined)}
      />
    </div>
  )
}
