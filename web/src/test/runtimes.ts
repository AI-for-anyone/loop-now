import type { Schemas } from '@/api/client'
import { runtimeLookup } from '@/lib/runtime'

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

// Fixture for GET /runtime-definitions: the two runtimes as the server defines them.
export function runtimeDefinitions(): Schemas['RuntimeDefinitionList'] {
  return {
    runtimes: [
      { id: 'pi', name: 'Pi', providers: [], isDefault: true, capabilities: { accounts: 'managed', realHome: false, nativeCommands: true, pluginExtensions: true, bundled: true }, setup: null },
      { id: 'claude-code', name: 'Claude Code', providers: ['claude-code'], isDefault: false, capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false }, setup: { install: 'curl -fsSL https://claude.ai/install.sh | bash', login: 'claude', docsUrl: 'https://docs.anthropic.com/en/docs/claude-code/setup' } },
    ],
  }
}

// Runtime names as the default definitions give them, for building run log steps in tests.
export const runtimeName = runtimeLookup(runtimeDefinitions().runtimes).name
