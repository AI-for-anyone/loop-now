import type { Schemas } from '@/api/client'
import { defaultProject } from './api'
export const importCandidate = (overrides: Partial<Schemas['CLIImportCandidate']> = {}): Schemas['CLIImportCandidate'] => ({ candidateId: '51000000-0000-7000-8000-000000000001', cli: 'gh', sourceKind: 'gh_hosts', label: '合成 GitHub 账号', status: 'may_import', missingFields: [], supplementableFields: [], reasonCode: null, previouslyImported: false, ...overrides })
export function importScan(overrides: Partial<Schemas['CLIImportScan']> = {}): Schemas['CLIImportScan'] {
  return { scanId: '52000000-0000-7000-8000-000000000001', projectId: defaultProject.id, expiresAt: '2030-10-16T10:00:00Z', credentialsRevision: 5, sources: ['gh', 'mysql', 'psql', 'gcloud', 'lark-cli'].map(cli => ({ cli: cli as Schemas['CLIImportCLI'], status: 'candidates', reasonCode: null })), candidates: [importCandidate()], ...overrides }
}
export function importOperation(overrides: Partial<Schemas['CLIImportOperation']> = {}): Schemas['CLIImportOperation'] {
  const scan = importScan(), candidate = scan.candidates[0]
  return { id: '53000000-0000-7000-8000-000000000001', projectId: scan.projectId, scanId: scan.scanId, status: 'running', phase: 'extracting', createdAt: '2026-10-16T10:00:00Z', deadlineAt: '2030-10-16T10:00:00Z', completedAt: null, items: [{ candidateId: candidate.candidateId, cli: candidate.cli, label: candidate.label, status: 'pending', accountId: null, versionId: null, errorCode: null }], errorCode: null, credentialsRevision: null, ...overrides }
}
