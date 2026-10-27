import createClient from 'openapi-fetch'
import { config } from '@/config'
import type { components, paths } from './schema.gen'

export type Schemas = components['schemas']
export type ErrorCode = Schemas['ErrorCode']

// 服务端是否可达：最近一次请求收到任何 HTTP 响应即为可达，请求没有到达服务端时为不可达。
// 全局连接提示订阅这里，覆盖查询、修改以及不进入查询缓存的凭证提交。
let reachable = true
const reachabilityListeners = new Set<() => void>()

function setReachable(next: boolean) {
  if (reachable === next) return
  reachable = next
  reachabilityListeners.forEach((listener) => listener())
}

export const reachability = {
  isReachable: () => reachable,
  subscribe(listener: () => void) {
    reachabilityListeners.add(listener)
    return () => {
      reachabilityListeners.delete(listener)
    }
  },
  // 只供测试在用例之间复位。
  reset: () => setReachable(true),
}

// 服务端版本：取自最近一次响应的 Loop-Now-Version 头（跨域时服务端经 Access-Control-Expose-Headers 暴露），全局设置中显示。
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
  // 只供测试在用例之间复位。
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
  // 每次请求时再取 globalThis.fetch，测试中替换的 fetch 才能生效。
  fetch: async (request) => {
    try {
      const response = await globalThis.fetch(request)
      setReachable(true)
      noteVersion(response)
      return response
    } catch (cause) {
      // 主动取消的请求不说明服务端是否可达。
      if (!(cause instanceof DOMException && cause.name === 'AbortError')) setReachable(false)
      throw cause
    }
  },
})

type ErrorDetails = NonNullable<Schemas['Error']['error']['details']>

// 控制台内统一的接口错误。status 为 0 表示请求没有到达服务端，或被跨域拦截。
export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | 'unreachable' | 'unknown'
  readonly serverMessage: string | null
  // 按 code 定义：agent_busy 带 AgentBusyDetails，删除项目时的 agent_not_idle 带 ProjectNotIdleDetails。
  readonly details: ErrorDetails | undefined
  readonly fields: Schemas['FieldError'][]
  // 响应头 Loop-Now-Request-Id，与服务端日志中的 requestId 一致；请求没有到达服务端时为 null。
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
  'session_missing',
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

// 从响应中取请求编号（跨域时服务端经 Access-Control-Expose-Headers 暴露）。
export function requestIdOf(response: Response): string | null {
  return response.headers.get(REQUEST_ID_HEADER)
}

interface FetchResult<T> {
  data?: T
  error?: unknown
  response: Response
}

// 执行一次 openapi-fetch 请求：成功时返回 data，失败时抛出 ApiError。
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

// 以原始字节发送请求体（资料上传与在线编辑）。
export function rawBody(content: Blob) {
  return {
    body: content as unknown as string,
    bodySerializer: (body: unknown) => body as Blob,
    headers: { 'Content-Type': 'application/octet-stream' },
  }
}

// 以 multipart 发送 Skill 压缩包，字段名为 archive。
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
