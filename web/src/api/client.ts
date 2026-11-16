import createClient from 'openapi-fetch'
import { config } from '@/config'
import type { components, paths } from './schema.gen'

export type Schemas = components['schemas']
export type ErrorCode = Schemas['ErrorCode']

// Whether the server is reachable: reachable if the most recent request got any HTTP response, unreachable if the request did not reach
// the server.
// The global connection notice subscribes here, which covers queries, mutations, and the credential submissions that skip the query cache.
// "Most recent" goes by the order in which requests started: a request that started before one that already has a result is stale
// however late it finishes, and no longer changes reachability
// (e.g. a request sent while disconnected that only fails after the connection is back must not bring back a connection notice that
// has already gone away).
let reachable = true
let started = 0
let settled = 0
const reachabilityListeners = new Set<() => void>()

function setReachable(next: boolean) {
  if (reachable === next) return
  reachable = next
  reachabilityListeners.forEach((listener) => listener())
}

function settle(request: number, next: boolean) {
  if (request < settled) return
  settled = request
  setReachable(next)
}

export const reachability = {
  isReachable: () => reachable,
  subscribe(listener: () => void) {
    reachabilityListeners.add(listener)
    return () => {
      reachabilityListeners.delete(listener)
    }
  },
  // Only for tests, to reset between cases: requests from earlier cases that have not finished are all treated as stale.
  reset: () => {
    settled = started
    setReachable(true)
  },
}

// Server version: taken from the Loop-Now-Version header of the most recent response (for cross-origin requests the server exposes it via
// Access-Control-Expose-Headers), shown in global settings.
export const VERSION_HEADER = 'Loop-Now-Version'
let serverVersion: string | null = null
const versionListeners = new Set<() => void>()

export const serverVersionStore = {
  get: () => serverVersion,
  subscribe(listener: () => void) {
    versionListeners.add(listener)
    return () => {
      versionListeners.delete(listener)
    }
  },
  // Only for tests, to reset between cases.
  reset: () => {
    serverVersion = null
  },
}

function noteVersion(response: Response) {
  const version = response.headers.get(VERSION_HEADER)
  if (!version || version === serverVersion) return
  serverVersion = version
  versionListeners.forEach((listener) => listener())
}

export const api = createClient<paths>({
  baseUrl: config.apiBase,
  // Read globalThis.fetch on every request so that a fetch replaced in tests takes effect.
  fetch: async (request) => {
    const order = ++started
    try {
      const response = await globalThis.fetch(request)
      settle(order, true)
      noteVersion(response)
      return response
    } catch (cause) {
      // A request we aborted ourselves says nothing about whether the server is reachable. Check the request's own signal: the DOMException
      // thrown on abort may come from another realm (in tests, jsdom and Node each have one), so instanceof is unreliable.
      if (!request.signal.aborted) settle(order, false)
      throw cause
    }
  },
})

type ErrorDetails = NonNullable<Schemas['Error']['error']['details']>

// The console's single API error type. status 0 means the request did not reach the server, or was blocked by CORS.
export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | 'unreachable' | 'unknown'
  readonly serverMessage: string | null
  // Defined per code: agent_busy carries AgentBusyDetails, and agent_not_idle when deleting a project carries ProjectNotIdleDetails.
  readonly details: ErrorDetails | undefined
  readonly fields: Schemas['FieldError'][]
  // The Loop-Now-Request-Id response header, matching requestId in the server logs; null when the request did not reach the server.
  readonly requestId: string | null

  constructor(init: {
    status: number
    code: ApiError['code']
    serverMessage?: string | null
    details?: ErrorDetails
    fields?: Schemas['FieldError'][]
    requestId?: string | null
  }) {
    super(init.serverMessage ?? init.code)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.serverMessage = init.serverMessage ?? null
    this.details = init.details
    this.fields = init.fields ?? []
    this.requestId = init.requestId ?? null
  }

  fieldMessage(field: string): string | undefined {
    return this.fields.find((f) => f.field === field)?.message
  }
}

const ERROR_CODES = new Set<string>([
  'import_operation_conflict',
  'import_in_progress',
  'import_scan_expired',
  'import_source_conflict',
  'import_provenance_key_changed',
  'import_duplicate_source',
  'import_source_changed',
  'verification_in_progress',
  'verification_operation_conflict',
  'verification_unsupported',
  'credential_selection_required',
  'credential_account_deleted',
  'credential_scope_mismatch',
  'project_credential_required',
  'model_auth_in_progress',
  'sleep_conflict',
  'already_answered',
  'question_expired',
  'question_closed',
  'not_found',
  'validation_failed',
  'precondition_failed',
  'payload_too_large',
  'agent_busy',
  'agent_not_idle',
  'session_none',
  'session_missing',
  'console_command_failed',
  'template_deleted',
  'name_conflict',
  'path_conflict',
  'forbidden_origin',
  'invalid_host',
  'unauthenticated',
  'forbidden',
  'internal_error',
  'task_conflict',
  'run_not_queued',
  'run_not_active',
  'action_conflict',
  'task_not_idle',
  'model_auth_busy',
  'plugin_source_failed',
  'plugin_confirmation_required',
  'plugin_version_conflict',
  'plugin_not_enabled',
  'plugin_blocked',
  'plugin_failed',
  'plugin_timeout',
] satisfies ErrorCode[])

function isErrorBody(value: unknown): value is Schemas['Error'] {
  if (typeof value !== 'object' || value === null || !('error' in value)) return false
  const inner = (value as { error: unknown }).error
  return typeof inner === 'object' && inner !== null && typeof (inner as { code?: unknown }).code === 'string'
}

export const REQUEST_ID_HEADER = 'Loop-Now-Request-Id'

export function toApiError(status: number, body: unknown, requestId: string | null = null): ApiError {
  if (isErrorBody(body)) {
    const { code, message, details, fields } = body.error
    return new ApiError({
      status,
      code: ERROR_CODES.has(code) ? code : 'unknown',
      serverMessage: message,
      details,
      fields,
      requestId,
    })
  }
  return new ApiError({ status, code: 'unknown', serverMessage: typeof body === 'string' && body !== '' ? body : null, requestId })
}

// Get the request ID from a response (for cross-origin requests the server exposes it via Access-Control-Expose-Headers).
export function requestIdOf(response: Response): string | null {
  return response.headers.get(REQUEST_ID_HEADER)
}

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

// Run one openapi-fetch request: return data on success, throw ApiError on failure.
export async function call<T>(request: Promise<FetchResult<T>>): Promise<T> {
  let result: FetchResult<T>
  try {
    result = await request
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
    throw new ApiError({ status: 0, code: 'unreachable' })
  }
  if (!result.response.ok) throw toApiError(result.response.status, result.error, requestIdOf(result.response))
  return result.data as T
}

// Send the request body as raw bytes (file uploads and in-place editing).
export function rawBody(content: Blob) {
  return {
    body: content as unknown as string,
    bodySerializer: (body: unknown) => body as Blob,
    headers: { 'Content-Type': 'application/octet-stream' },
  }
}

// Send a Skill archive as multipart, in a field named archive.
export function archiveBody(archive: Blob, filename = 'skill.zip') {
  return {
    body: { archive: archive as unknown as string },
    bodySerializer: () => {
      const form = new FormData()
      form.append('archive', archive, filename)
      return form
    },
  }
}
