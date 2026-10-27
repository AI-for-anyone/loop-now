import { ApiError } from '@/api/client'
import { t } from '@/i18n'

// 把任意错误转换为给人看的中文说明：已知错误码用文案表，未知时用服务端给出的说明。
export function describeError(error: unknown): string {
  if (!(error instanceof ApiError)) return t.errors.unknown
  if (error.code === 'unreachable') return t.errors.unreachable
  if (error.code === 'unknown') return error.serverMessage ?? t.errors.unknownStatus(error.status)
  const known = t.errors.code[error.code]
  return error.serverMessage && error.serverMessage !== error.code && error.serverMessage !== known
    ? t.errors.withDetail(known, error.serverMessage)
    : known
}

// 出错请求的请求编号（响应头 Loop-Now-Request-Id）；没有时为 null。
export function requestIdOf(error: unknown): string | null {
  return error instanceof ApiError ? error.requestId : null
}

export function isNotFound(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404
}

export function isUnreachable(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'unreachable'
}

export function fieldError(error: unknown, field: string): string | undefined {
  return error instanceof ApiError ? error.fieldMessage(field) : undefined
}

// 服务端对模型的校验错误可能落在 model、model.provider 或 model.modelId 上，都显示在模型选择旁。
export const isModelField = (field: string) => field === 'model' || field === 'model.provider' || field === 'model.modelId'

// 合并匹配字段的全部校验错误，用于显示在对应输入旁。
export function fieldMessages(error: unknown, match: (field: string) => boolean): string | undefined {
  if (!(error instanceof ApiError)) return undefined
  const messages = error.fields.filter((f) => match(f.field)).map((f) => f.message)
  return messages.length > 0 ? messages.join(t.errors.fieldSeparator) : undefined
}

// 去掉已在输入旁显示的字段错误，返回还需要整体显示的错误：
// 没有字段级错误时原样返回；字段错误都已显示时返回 null；否则列出全部剩余字段。
export function unmatchedFieldError(error: unknown, shown: (field: string) => boolean): unknown {
  if (!(error instanceof ApiError) || error.fields.length === 0) return error
  const rest = error.fields.filter((f) => !shown(f.field))
  if (rest.length === 0) return null
  return new ApiError({
    status: error.status,
    code: error.code,
    serverMessage: rest.map((f) => t.errors.field(f.field, f.message)).join(t.errors.fieldSeparator),
    details: error.details,
    fields: rest,
    requestId: error.requestId,
  })
}

// Agent 名称在所属项目内或平台内唯一：服务端以 409 name_conflict 拒绝，不带字段级错误，这里把它归到名称字段上。
export function agentNameError(error: unknown, scope: 'project' | 'platform' = 'project'): string | undefined {
  if (error instanceof ApiError && error.code === 'name_conflict')
    return scope === 'platform' ? t.createAgent.platformNameConflict : t.createAgent.nameConflict
  return fieldError(error, 'name')
}

export function isAgentNameConflict(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'name_conflict'
}

// 项目名称在组织内唯一：重名同样以 409 name_conflict 拒绝、不带字段级错误，归到名称字段上。
export function projectNameError(error: unknown): string | undefined {
  if (error instanceof ApiError && error.code === 'name_conflict') return t.projects.create.nameConflict
  return fieldError(error, 'name')
}
