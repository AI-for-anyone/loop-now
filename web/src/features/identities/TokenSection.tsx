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
import { IssueTokenDialog } from './IssueTokenDialog'

// 令牌区块：表格列出全部令牌（含已吊销与已过期的，新的在前），有效的可以单独吊销；签发在对话框中进行。
export function TokenSection({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const tokens = useIdentityTokens(identity.id, true)
  const revoke = useRevokeToken(identity.id)
  const [issuing, setIssuing] = useState(false)
  const [revoking, setRevoking] = useState<Schemas['ExternalToken'] | null>(null)
  const list = tokens.data?.tokens ?? []

  return (
    <section aria-labelledby="identity-tokens" className="flex flex-col gap-3.5 rounded-panel bg-paper-raised px-6 py-[22px] shadow-print-rest">
      <div className="flex flex-wrap items-center gap-2.5">
        <h3 id="identity-tokens" className="font-display-cn text-[30px] leading-[1.15] font-normal">
          {t.identities.tokens}
          {tokens.data && (
            <span aria-hidden="true" className="ml-2 font-display-latin text-[20px] font-extrabold text-ink-500">
              {list.length}
            </span>
          )}
        </h3>
        <Button className="ml-auto" onClick={() => setIssuing(true)}>
          {t.identities.issue}
        </Button>
      </div>
      {tokens.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
      {tokens.isError && <ErrorAlert error={tokens.error} />}
      {tokens.isSuccess && list.length === 0 && <p className="text-[14px] text-ink-500">{t.identities.noTokens}</p>}
      {list.length > 0 && (
        <Table className="min-w-[700px] text-[13.5px]">
          <TableHeader>
            <TableRow>
              <TableHead scope="col">{t.identities.columns.prefix}</TableHead>
              <TableHead scope="col">{t.identities.columns.createdAt}</TableHead>
              <TableHead scope="col">{t.identities.columns.expiresAt}</TableHead>
              <TableHead scope="col">{t.identities.columns.lastUsedAt}</TableHead>
              <TableHead scope="col">{t.identities.columns.status}</TableHead>
              <TableHead scope="col">
                <span className="sr-only">{t.identities.columns.actions}</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.map((token) => {
              const state = tokenState(token)
              return (
                <TableRow key={token.id}>
                  <TableCell className="font-mono text-[13px] font-bold whitespace-nowrap">{token.prefix}</TableCell>
                  <TableCell className="whitespace-nowrap">{t.common.time(token.createdAt)}</TableCell>
                  <TableCell className="whitespace-nowrap">{token.expiresAt ? t.common.time(token.expiresAt) : t.identities.never}</TableCell>
                  <TableCell className="whitespace-nowrap">{token.lastUsedAt ? t.common.time(token.lastUsedAt) : t.identities.neverUsed}</TableCell>
                  <TableCell>
                    <Badge tone={state === 'active' ? 'success' : 'warning'}>{t.identities.status[state]}</Badge>
                  </TableCell>
                  <TableCell className="text-right">
                    {state === 'active' && (
                      <Button variant="secondary" size="xs" aria-label={`${t.identities.revoke} ${token.prefix}`} onClick={() => setRevoking(token)}>
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
