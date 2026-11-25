import type { Schemas } from '@/api/client'

// Fixtures for importing conversations: folders and conversations as the scan reports them.
let seq = 0
const next = () => String(++seq).padStart(4, '0')

export function makeImportConversation(overrides: Partial<Schemas['ImportConversation']> = {}): Schemas['ImportConversation'] {
  const n = next()
  return {
    cli: 'claude',
    sessionId: `session-${n}`,
    cwd: '/Users/me/code/app',
    title: `Conversation ${n}`,
    firstPrompt: `First prompt ${n}`,
    lastActiveAt: '2026-11-24T08:00:00Z',
    messageCount: 12,
    sizeBytes: 48_000,
    model: 'claude-sonnet-4-5',
    agentId: null,
    skipReason: null,
    ...overrides,
  }
}

export function makeImportFolder(overrides: Partial<Schemas['ImportFolder']> = {}): Schemas['ImportFolder'] {
  const path = overrides.path ?? '/Users/me/code/app'
  return {
    path,
    git: true,
    repository: { remote: 'git@github.com:me/app.git', mainCheckout: path },
    lastActiveAt: '2026-11-24T08:00:00Z',
    preChecked: true,
    projectId: null,
    conversations: [makeImportConversation({ cwd: path }), makeImportConversation({ cwd: path }), makeImportConversation({ cwd: path })],
    truncated: false,
    ...overrides,
  }
}

export function makeScan(overrides: Partial<Schemas['ConversationScan']> = {}): Schemas['ConversationScan'] {
  return { operationId: '61000000-0000-7000-8000-000000000001', state: 'done', error: null, filesTotal: 12, filesDone: 12, folders: [], ...overrides }
}

export function makeImport(overrides: Partial<Schemas['ConversationImport']> = {}): Schemas['ConversationImport'] {
  return { operationId: '62000000-0000-7000-8000-000000000001', state: 'running', error: null, done: 0, total: 0, projects: [], agents: [], errors: [], ...overrides }
}
