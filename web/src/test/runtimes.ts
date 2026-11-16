import type { Schemas } from '@/api/client'

const checkedAt = '2026-11-11T08:00:00Z'

// Fixture for GET /runtimes: pi is always available; claude-code is installed and logged in by default, and tests override this as needed.
export function runtimeStatus(id: Schemas['AgentRuntime'], overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return id === 'pi'
    ? { id, available: true, version: '1.1.0', binaryPath: null, unavailableReason: null, login: null, checkedAt, ...overrides }
    : { id, available: true, version: '2.1.0', binaryPath: '/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js', unavailableReason: null, login: { state: 'logged_in', authMethod: 'claude.ai', subscriptionType: 'max' }, checkedAt, ...overrides }
}

export function runtimeList(claudeCode: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeList'] {
  return { runtimes: [runtimeStatus('pi'), runtimeStatus('claude-code', claudeCode)] }
}
