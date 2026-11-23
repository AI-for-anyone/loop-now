import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody } from '@/test/api'
import { verificationAccount, verificationOperation } from '@/test/cli-verification'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { CLIConnectionVerification } from '@/features/project-credentials/CLIConnectionVerification'
import { t } from '@/i18n'

const account = verificationAccount()
const base = `/projects/${account.projectId}/credential-accounts/${account.id}/verification-operations`
function setup(initial: Schemas['CLIVerificationOperation'] | null = null) {
  let op = initial
  const posts: unknown[] = [], reads: string[] = []
  server.use(http.get(apiUrl(base), () => HttpResponse.json({ accountId: account.id, currentVersionId: account.currentVersionId, operation: op })),
    http.get(apiUrl(`${base}/:id`), ({ params }) => { reads.push(String(params.id)); return op ? HttpResponse.json(op) : HttpResponse.json(errorBody('not_found'), { status: 404 }) }),
    http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; posts.push(body); op = verificationOperation({ id: body.operationId }); return HttpResponse.json(op, { status: 202 }) }))
  return { posts, reads, set: (next: typeof op) => { op = next } }
}

// Older servers had no unknown_endpoint signal and answered a missing endpoint with the fixed Chinese text “接口不存在” (No such
// endpoint); that text alone marks the server as old. The unknown_endpoint case is in
// src/features/project-credentials/CLIConnectionVerification.test.tsx.
describe('Manual verification for five tools, formal HTTP fixture', () => {
  it.each([
    ["an older server's fixed text", errorBody('not_found', '接口不存在')],
  ])('only an old router\'s fixed missing-endpoint 404 means unsupported (%s)', async (_, body) => {
    setup(); server.use(http.get(apiUrl(base), () => HttpResponse.json(body, { status: 404 })))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.unsupported); expect(screen.queryByText(t.credentials.verification.empty)).toBeNull()
  })
})
