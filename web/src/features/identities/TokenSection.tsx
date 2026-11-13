import { useState } from 'react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useIdentityTokens, useRevokeToken } from '@/api/inbox'
import { t } from '@/i18n'
import { tokenState } from './identity-rules'
import { cn } from '@/lib/utils'

const headClass = 'py-2 pl-1 text-[12px] font-black text-ink-500'
import { IssueTokenDialog } from './IssueTokenDialog'

// 令牌区块：表格列出全部令牌（含已吊销与已过期的，新的在前），有效的可以单独吊销；签发在对话框中进行。
// 改版：直接排在纸上，标题小一号；表头下一道实线、行间虚线（通用规则第 9 条），状态是胶囊，“吊销”是红字。
export function TokenSection({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const tokens = useIdentityTokens(identity.id, true)
  const revoke = useRevokeToken(identity.id)
  const [issuing, setIssuing] = useState(false)
  const [revoking, setRevoking] = useState<Schemas['ExternalToken'] | null>(null)
  const list = tokens.data?.tokens ?? []

  return (
    <section aria-labelledby="identity-tokens" className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2.5">
        <h3 id="identity-tokens" className="text-[17px] leading-[1.3] font-black">
          {t.identities.tokens}
          {tokens.data && (
            <span aria-hidden="true" className="ml-2 font-display-latin text-[15px] font-extrabold text-ink-500">
              {list.length}
            </span>
          )}
        </h3>
        <Button size="sm" className="ml-auto border-2 border-ink shadow-[3px_3px_0_var(--ink)]" onClick={() => setIssuing(true)}>
          {t.identities.issue}
        </Button>
      </div>
      {tokens.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
      {tokens.isError && <ErrorAlert error={tokens.error} />}
      {tokens.isSuccess && list.length === 0 && <p className="text-[14px] text-ink-500">{t.identities.noTokens}</p>}
      {list.length > 0 && (
        <Table className="min-w-[700px] text-[13.5px]" card={false}>
          <TableHeader className="bg-transparent">
            <TableRow className="border-b-2 border-solid border-ink last:border-b-2">
              <TableHead scope="col" className={headClass}>{t.identities.columns.prefix}</TableHead>
              <TableHead scope="col" className={headClass}>{t.identities.columns.createdAt}</TableHead>
              <TableHead scope="col" className={headClass}>{t.identities.columns.expiresAt}</TableHead>
              <TableHead scope="col" className={headClass}>{t.identities.columns.lastUsedAt}</TableHead>
              <TableHead scope="col" className={headClass}>{t.identities.columns.status}</TableHead>
              <TableHead scope="col" className={headClass}>
                <span className="sr-only">{t.identities.columns.actions}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((token) => {
              const state = tokenState(token)
              return (
                <TableRow key={token.id} className={cn('border-b-[1.5px] border-ink/25 last:border-b-[1.5px]', state !== 'active' && 'text-ink-500')}>
                  <TableCell className="py-3 pl-1 font-mono text-[12.5px] whitespace-nowrap">{token.prefix}</TableCell>
                  <TableCell className="py-3 font-mono text-[12px] whitespace-nowrap">{t.common.time(token.createdAt)}</TableCell>
                  <TableCell className="py-3 whitespace-nowrap">{token.expiresAt ? t.common.time(token.expiresAt) : t.identities.never}</TableCell>
                  <TableCell className="py-3 whitespace-nowrap">{token.lastUsedAt ? t.common.time(token.lastUsedAt) : t.identities.neverUsed}</TableCell>
                  <TableCell className="py-3">
                    <Badge tone={state === 'active' ? 'success' : 'neutral'}>{t.identities.status[state]}</Badge>
                  </TableCell>
                  <TableCell className="py-3 pr-1 text-right">
                    {state === 'active' && (
                      <Button
                        variant="ghost"
                        size="xs"
                        className="h-7 px-1.5 text-[13px] text-red-text underline decoration-red-text decoration-2 underline-offset-4 hover:bg-tint-red"
                        aria-label={`${t.identities.revoke} ${token.prefix}`}
                        onClick={() => setRevoking(token)}
                      >
                        {t.identities.revoke}
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
      <ConfirmDialog
        open={revoking !== null}
        onOpenChange={(open) => !open && setRevoking(null)}
        title={revoking ? t.identities.revokeTitle(revoking.prefix) : ''}
        body={t.identities.revokeBody(identity.name)}
        confirmLabel={t.identities.revoke}
        destructive
        onConfirm={() => (revoking ? revoke.mutateAsync(revoking.id) : undefined)}
      />
      {issuing && <IssueTokenDialog identity={identity} onClose={() => setIssuing(false)} />}
    </section>
  )
}
