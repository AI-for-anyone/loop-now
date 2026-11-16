import { ApiError } from './client'

// Fixed text from the old server protocol, used to recognize it for compatibility; not translatable UI text.
export const backendSignals = { missingEndpoint: '接口不存在' } as const

// Whether a request failed because this server has no such endpoint (an older server), as opposed to a missing resource.
// This is the only place that recognizes it: when the server gains a machine-readable signal, check it here and keep the
// old text for older servers.
export function isMissingEndpoint(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404 && error.code === 'not_found' && error.serverMessage === backendSignals.missingEndpoint
}
