import { ApiError } from './client'

// Fixed text from the old server protocol, used to recognize it for compatibility; not translatable UI text.
export const backendSignals = { missingEndpoint: '接口不存在' } as const

// Whether a request failed because this server has no such endpoint (an older server), as opposed to a missing resource.
// The server marks it with details.reason === 'unknown_endpoint' on a 404 not_found. Servers from before that signal only said
// it in their fixed message text; that check stays for them. This is the only place that recognizes it.
export function isMissingEndpoint(error: unknown): boolean {
  if (!(error instanceof ApiError) || error.status !== 404 || error.code !== 'not_found') return false
  return notFoundReason(error.details) === 'unknown_endpoint' || error.serverMessage === backendSignals.missingEndpoint
}

// details.reason of a not_found error. Read as an unknown shape: the generated contract on this branch does not have
// NotFoundDetails yet.
function notFoundReason(details: unknown): unknown {
  return typeof details === 'object' && details !== null && 'reason' in details ? (details as { reason: unknown }).reason : undefined
}
