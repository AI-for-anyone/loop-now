import createClient from 'openapi-fetch'
import { absoluteApiBase, config } from '@/config'
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

// Whether the remote door rejected a request as unauthenticated (401): missing, removed or expired device session.
// On the local door no route requires a device session (door === 'local'), so an isolated 401 never locks out the console.
// authEpoch stops a stale 401 from before pairing from locking a newly paired session.
type Door = Schemas['Context']['door']
let unauthenticated = false
let knownDoor: Door | null = null
let authEpoch = 0
const authListeners = new Set<() => void>()

function setUnauthenticated(next: boolean) {
  if (unauthenticated === next) return
  unauthenticated = next
  authListeners.forEach((listener) => listener())
}

function isContextRequest(url: string): boolean {
  try {
    return new URL(url, 'http://localhost').pathname.endsWith('/context')
  } catch {
    return url.endsWith('/context')
  }
}

export const deviceAuth = {
  isUnauthenticated: () => unauthenticated,
  door: () => knownDoor,
  noteDoor: (door: Door) => {
    knownDoor = door
  },
  markUnauthenticated: () => setUnauthenticated(true),
  markAuthenticated: () => {
    authEpoch += 1
    setUnauthenticated(false)
  },
  subscribe(listener: () => void) {
    authListeners.add(listener)
    return () => {
      authListeners.delete(listener)
    }
  },
  // Only for tests, to reset between cases.
  reset: () => {
    authEpoch += 1
    knownDoor = null
    setUnauthenticated(false)
  },
}

function isLoopbackHost(hostname: string): boolean {
  return hostname === '127.0.0.1' || hostname === 'localhost' || hostname === '::1' || hostname === '[::1]'
}

export function isRemoteDoorOrigin(door: Door | null | undefined, apiBase: string, origin: string): boolean {
  if (door === 'remote') return true
  if (door === 'local') return false
  // Before /context has answered (e.g. a phone opening https://<id>.loop-now.app while the host is already offline),
  // a same-origin console on a non-loopback host is served by the remote door through a tunnel.
  try {
    const pageUrl = new URL(origin)
    const apiUrl = new URL(apiBase)
    return pageUrl.origin === apiUrl.origin && !isLoopbackHost(pageUrl.hostname)
  } catch {
    return false
  }
}

function isGatewayStatus(status: number): boolean {
  return status === 502 || status === 503 || status === 504 || (status >= 520 && status <= 530)
}

function isJsonContentType(contentType: string | null): boolean {
  const ct = (contentType ?? '').toLowerCase()
  return ct.includes('application/json') || ct.includes('+json')
}

function isHtmlContentType(contentType: string | null): boolean {
  const ct = (contentType ?? '').toLowerCase()
  return ct.includes('text/html') || ct.includes('application/xhtml')
}

function isCurrentlyRemoteDoor(): boolean {
  if (typeof window === 'undefined') return knownDoor === 'remote'
  return isRemoteDoorOrigin(knownDoor, absoluteApiBase(), window.location.origin)
}

// Whether an HTTP response came from a gateway/intermediary (such as Cloudflare answering 530 or a 502 HTML page when the computer is
// offline) instead of the Loop Now server. Every genuine Loop Now response carries Loop-Now-Version (written by withCommonHeaders).
export function isUnreachableHttpResponse(info: {
  status: number
  contentType: string | null
  hasVersionHeader: boolean
}): boolean {
  if (info.hasVersionHeader) return false
  const { status, contentType } = info
  if (isCurrentlyRemoteDoor()) {
    if (serverVersion !== null) return true
    if (isGatewayStatus(status)) return true
    if ((status < 200 || status >= 300) && !isJsonContentType(contentType)) return true
    if (isHtmlContentType(contentType)) return true
    return false
  }
  if (status >= 520 && status <= 530) return true
  if (isGatewayStatus(status) && isHtmlContentType(contentType)) return true
  return false
}

export function isUnreachableResponse(response: Response): boolean {
  return isUnreachableHttpResponse({
    status: response.status,
    contentType: response.headers.get('content-type'),
    hasVersionHeader: Boolean(response.headers.get(VERSION_HEADER)),
  })
}

export const api = createClient<paths>({
  baseUrl: config.apiBase,
  // Read globalThis.fetch on every request so that a fetch replaced in tests takes effect.
  fetch: async (request) => {
    const order = ++started
    const epoch = authEpoch
    try {
      const response = await globalThis.fetch(request)
      const unreachable = isUnreachableResponse(response)
      settle(order, !unreachable)
      if (!unreachable) {
        noteVersion(response)
        if (response.status === 401 && epoch === authEpoch && (knownDoor === 'remote' || (knownDoor === null && isContextRequest(request.url)))) {
          setUnauthenticated(true)
        }
      }
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

// Every code in the contract, as an exhaustive record: a code added to the contract but missing here is a type error,
// instead of being silently turned into 'unknown' (which once happened to cli_auth_in_progress and
// credential_selection_mode_conflict).
const ERROR_CODES: Record<ErrorCode, true> = {
  import_operation_conflict: true,
  import_in_progress: true,
  import_scan_expired: true,
  import_source_conflict: true,
  import_provenance_key_changed: true,
  import_duplicate_source: true,
  import_source_changed: true,
  verification_in_progress: true,
  verification_operation_conflict: true,
  verification_unsupported: true,
  credential_selection_required: true,
  credential_account_deleted: true,
  credential_scope_mismatch: true,
  project_credential_required: true,
  model_auth_in_progress: true,
  sleep_conflict: true,
  already_answered: true,
  question_expired: true,
  question_closed: true,
  not_found: true,
  validation_failed: true,
  precondition_failed: true,
  payload_too_large: true,
  agent_busy: true,
  agent_not_idle: true,
  session_none: true,
  session_missing: true,
  console_command_failed: true,
  template_deleted: true,
  name_conflict: true,
  path_conflict: true,
  forbidden_origin: true,
  invalid_host: true,
  unauthenticated: true,
  forbidden: true,
  internal_error: true,
  task_conflict: true,
  run_not_queued: true,
  run_not_active: true,
  action_conflict: true,
  task_not_idle: true,
  model_auth_busy: true,
  plugin_source_failed: true,
  plugin_confirmation_required: true,
  plugin_version_conflict: true,
  plugin_not_enabled: true,
  plugin_blocked: true,
  plugin_failed: true,
  plugin_timeout: true,
  runtime_in_use: true,
  text_generation_unavailable: true,
  text_generation_failed: true,
  path_picker_unavailable: true,
  path_picker_busy: true,
  remote_access_off: true,
  pairing_code_invalid: true,
  rate_limited: true,
  relay_signed_in: true,
  relay_signed_out: true,
  relay_failed: true,
  background_task_not_found: true,
  background_task_ended: true,
  task_workspace_not_kept: true,
  permission_request_answered: true,
  cli_auth_in_progress: true,
  credential_selection_mode_conflict: true,
}
const isErrorCode = (code: string): code is ErrorCode => Object.hasOwn(ERROR_CODES, code)

function isErrorBody(value: unknown): value is Schemas['Error'] {
  if (typeof value !== 'object' || value === null || !('error' in value)) return false
  const inner = (value as { error: unknown }).error
  return typeof inner === 'object' && inner !== null && typeof (inner as { code?: unknown }).code === 'string'
}

export const REQUEST_ID_HEADER = 'Loop-Now-Request-Id'

// Raw HTML error pages (e.g. Cloudflare 502/530 pages or proxy pages) must never be shown verbatim as error text.
function looksLikeHtml(text: string): boolean {
  const trimmed = text.replace(/^\s*(?:<!--[\s\S]*?-->\s*)*/u, '')
  return /^<(?:!doctype|html|head|body|title|meta|div|h[1-6]|p|pre|center|section|main|article)\b/i.test(trimmed)
}

export function toApiError(status: number, body: unknown, requestId: string | null = null): ApiError {
  if (isErrorBody(body)) {
    const { code, message, details, fields } = body.error
    return new ApiError({
      status,
      code: isErrorCode(code) ? code : 'unknown',
      serverMessage: message,
      details,
      fields,
      requestId,
    })
  }
  const raw = typeof body === 'string' ? body.trim() : ''
  const serverMessage = raw !== '' && !looksLikeHtml(raw) ? raw : null
  return new ApiError({ status, code: 'unknown', serverMessage, requestId })
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
  if (isUnreachableResponse(result.response)) {
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
