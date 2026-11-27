import { useState } from 'react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { useIdentityTokens, useRevokeToken } from '@/api/inbox'
import { t } from '@/i18n'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { tokenState } from './identity-rules'
import { IssueTokenDialog } from './IssueTokenDialog'

type TokenFilter = 'active' | 'all'

const headClass = 'py-2 px-2 first:pl-1 last:pr-1 text-[12px] font-semibold text-ink-500'

// Tokens table: lists active or all tokens (newest first) with an inline Active | All status filter; active tokens can be revoked
// individually; issuing happens in a dialog. Fits comfortably at 1280px without horizontal overflow.
export function TokenSection({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const tokens = useIdentityTokens(identity.id, true)
  const revoke = useRevokeToken(identity.id)
  const [filter, setFilter] = useState<TokenFilter>('all')
  const [issuing, setIssuing] = useState(false)
  const [revoking, setRevoking] = useState<Schemas['ExternalToken'] | null>(null)
  const list = tokens.data?.tokens ?? []
  const activeCount = list.filter((token) => tokenState(token) === 'active').length
  const visible = filter === 'active' ? list.filter((token) => tokenState(token) === 'active') : list

  return (
    <section aria-labelledby="identity-tokens" className="flex flex-col gap-3.5">
      <div className="flex flex-wrap items-center gap-2.5">
        <h3 id="identity-tokens" className="text-[15px] leading-[1.3] font-semibold">
          {t.identities.tokens}
          {tokens.data && (
            <span aria-hidden="true" className="ml-2 font-mono text-[12.5px] font-medium text-ink-500 tabular-nums">
              {list.length}
            </span>
          )}
        </h3>
        {list.length > 0 && (
          <ToggleGroup
            aria-label={t.identities.tokenFilter.label}
            className={cn(segmentedClass, 'h-7')}
            value={filter}
            onValueChange={(value) => {
              if (value === 'active' || value === 'all') setFilter(value)
            }}
          >
            <ToggleGroupItem value="active" className={cn(segmentedItemClass, 'px-2.5 text-[12px]')}>
              {t.identities.tokenFilter.active}
              <span className="ml-1 font-mono text-[11px] text-ink-500 tabular-nums">{activeCount}</span>
            </ToggleGroupItem>
            <ToggleGroupItem value="all" className={cn(segmentedItemClass, 'px-2.5 text-[12px]')}>
              {t.identities.tokenFilter.all}
              <span className="ml-1 font-mono text-[11px] text-ink-500 tabular-nums">{list.length}</span>
            </ToggleGroupItem>
          </ToggleGroup>
        )}
        <Button size="xs" className="ml-auto" onClick={() => setIssuing(true)}>
          {t.identities.issue}
        </Button>
      </div>
      {tokens.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
      {tokens.isError && <ErrorAlert error={tokens.error} />}
      {tokens.isSuccess && list.length === 0 && <p className="text-[14px] text-ink-500">{t.identities.noTokens}</p>}
      {tokens.isSuccess && list.length > 0 && visible.length === 0 && (
        <p className="text-[14px] text-ink-500">{t.identities.tokenFilter.emptyActive}</p>
      )}
      {visible.length > 0 && (
        <Table className="w-full text-[13px]" card={false}>
          <TableHeader className="bg-transparent">
            <TableRow className="border-b border-hairline-strong last:border-b">
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
            {visible.map((token) => {
              const state = tokenState(token)
              return (
                <TableRow
                  key={token.id}
                  className={cn(
                    'border-b border-hairline last:border-b',
                    state !== 'active' && 'text-ink-500',
                  )}
                >
                  <TableCell className="py-2.5 pr-2 pl-1 font-mono text-[12px] whitespace-nowrap">{token.prefix}</TableCell>
                  <TableCell className="px-2 py-2.5 font-mono text-[12px] whitespace-nowrap">{t.common.time(token.createdAt)}</TableCell>
                  <TableCell className="px-2 py-2.5 text-[12.5px] whitespace-nowrap">{token.expiresAt ? t.common.time(token.expiresAt) : t.identities.never}</TableCell>
                  <TableCell className="px-2 py-2.5 text-[12.5px] whitespace-nowrap">{token.lastUsedAt ? t.common.time(token.lastUsedAt) : t.identities.neverUsed}</TableCell>
                  <TableCell className="px-2 py-2.5">
                    <Badge tone={state === 'active' ? 'success' : 'neutral'}>{t.identities.status[state]}</Badge>
                  </TableCell>
                  <TableCell className="py-2.5 pr-1 pl-2 text-right">
                    {state === 'active' && (
                      <Button
                        variant="ghost"
                        size="xs"
                        className="h-7 px-1.5 text-[12.5px] text-red-text underline decoration-red-text decoration-1 underline-offset-4 hover:bg-tint-red"
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
