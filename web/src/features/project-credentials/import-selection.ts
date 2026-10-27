import type { Schemas } from '@/api/client'
import { importText } from '@/i18n/cli-import'

type Candidate = Schemas['CLIImportCandidate']
export type SupplementDraft = Pick<Schemas['CLIImportSupplement'], 'database' | 'sslMode' | 'caCert'>
export const nonzeroUUID = (value: string | null): value is string => !!value && /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const publicFields = ['database', 'sslMode', 'caCert'] as const
export function publicCertificatePEM(value: string) {
  // Only a public certificate envelope goes on the wire. The server performs
  // authoritative x509.ParseCertificate validation before accepting an import.
  let rest = value.trim(), count = 0
  while (rest) {
    const match = /^-----BEGIN CERTIFICATE-----\s*([A-Za-z0-9+/=\r\n\t ]+)\s*-----END CERTIFICATE-----/.exec(rest)
    if (!match) return false
    try { const bytes = atob(match[1].replace(/\s/g, '')); if (bytes.length < 4 || bytes.charCodeAt(0) !== 0x30) return false } catch { return false }
    rest = rest.slice(match[0].length).trim(); count++
  }
  return count > 0
}
export function supplementProblem(candidate: Candidate, draft: SupplementDraft): string | null {
  if (candidate.status !== 'may_import' && candidate.status !== 'needs_fields') return importText.candidate[candidate.status]
  const allowed = candidate.supplementableFields
  if (candidate.missingFields.some(field => !allowed.includes(field as typeof allowed[number]))) return importText.missingSecret
  for (const key of publicFields) {
    if (draft[key] && !allowed.includes(key)) return importText.errors.supplement_override
    if (candidate.missingFields.includes(key) && !draft[key]?.trim()) return importText.required
  }
  if (draft.database && draft.database.length > 1024) return importText.required
  if (draft.sslMode) {
    const modes = candidate.cli === 'mysql' ? ['DISABLED', 'REQUIRED', 'VERIFY_CA', 'VERIFY_IDENTITY'] : candidate.cli === 'psql' ? ['disable', 'require', 'verify-ca', 'verify-full'] : []
    if (!modes.includes(draft.sslMode)) return importText.invalidTLS
  }
  if (draft.caCert && (draft.caCert.length > 65536 || !publicCertificatePEM(draft.caCert))) return importText.invalidCA
  if (draft.sslMode && ['VERIFY_CA', 'VERIFY_IDENTITY', 'verify-ca', 'verify-full'].includes(draft.sslMode) && allowed.includes('caCert') && !draft.caCert?.trim()) return importText.verifyCA
  if (draft.caCert && ['DISABLED', 'disable'].includes(draft.sslMode ?? '')) return importText.invalidTLS
  return null
}
export function selectedSupplements(candidates: Candidate[], selected: string[], drafts: Record<string, SupplementDraft>): Schemas['CLIImportSupplement'][] {
  return candidates.filter(c => selected.includes(c.candidateId)).flatMap(candidate => {
    const draft = drafts[candidate.candidateId] ?? {}, supplement: Schemas['CLIImportSupplement'] = { candidateId: candidate.candidateId }
    for (const field of candidate.supplementableFields) if (draft[field]?.trim()) supplement[field] = draft[field]
    return Object.keys(supplement).length > 1 ? [supplement] : []
  })
}
