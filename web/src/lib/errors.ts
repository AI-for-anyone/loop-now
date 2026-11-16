import { ApiError } from '@/api/client'
import { t } from '@/i18n'
import { showServerText } from '@/i18n/locale'

// Turn any error into an explanation for people: known error codes use the message table, unknown ones use the explanation from the server.
// The server's explanations are only in Chinese, so when the UI is not Chinese they are no longer appended to known error codes.
export function describeError(error: unknown): string {
  if (!(error instanceof ApiError)) return t.errors.unknown
  if (error.code === 'unreachable') return t.errors.unreachable
  if (error.code === 'unknown') return error.serverMessage ?? t.errors.unknownStatus(error.status)
  const known = t.errors.code[error.code]
  return showServerText && error.serverMessage && error.serverMessage !== error.code && error.serverMessage !== known
    ? t.errors.withDetail(known, error.serverMessage)
    : known
}

// The request ID of the failed request (response header Loop-Now-Request-Id); null when there is none.
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

// The server's validation errors for the model may land on model, model.provider or model.modelId; all of them are shown next to the
// model picker.
export const isModelField = (field: string) => field === 'model' || field === 'model.provider' || field === 'model.modelId'

// Combine all validation errors of the matching fields, to show next to the corresponding input.
export function fieldMessages(error: unknown, match: (field: string) => boolean): string | undefined {
  if (!(error instanceof ApiError)) return undefined
  const messages = error.fields.filter((f) => match(f.field)).map((f) => f.message)
  return messages.length > 0 ? messages.join(t.errors.fieldSeparator) : undefined
}

// Remove the field errors already shown next to inputs and return the error that still needs to be shown as a whole:
// returned as is when there are no field-level errors; null when every field error is already shown; otherwise all remaining fields are
// listed.
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

// Agent names are unique within their project or the platform: the server rejects a duplicate with 409 name_conflict and no field-level
// error, so it is attributed to the name field here.
export function agentNameError(error: unknown, scope: 'project' | 'platform' = 'project'): string | undefined {
  if (error instanceof ApiError && error.code === 'name_conflict')
    return scope === 'platform' ? t.createAgent.platformNameConflict : t.createAgent.nameConflict
  return fieldError(error, 'name')
}

export function isAgentNameConflict(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'name_conflict'
}

// Project names are unique within the organization: a duplicate is likewise rejected with 409 name_conflict and no field-level error, and
// is attributed to the name field.
export function projectNameError(error: unknown): string | undefined {
  if (error instanceof ApiError && error.code === 'name_conflict') return t.projects.create.nameConflict
  return fieldError(error, 'name')
}
