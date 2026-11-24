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
      { id: 'pi', name: 'Pi', providers: [], isDefault: true, capabilities: { accounts: 'managed', realHome: false, nativeCommands: true, pluginExtensions: true, bundled: true }, minVersion: null, setup: null },
      { id: 'claude-code', name: 'Claude Code', providers: ['claude-code'], isDefault: false, capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false }, minVersion: null, setup: { install: 'curl -fsSL https://claude.ai/install.sh | bash', login: 'claude', docsUrl: 'https://docs.anthropic.com/en/docs/claude-code/setup' } },
    ],
  }
}

// Runtime names as the default definitions give them, for building run log steps in tests.
export const runtimeName = runtimeLookup(runtimeDefinitions().runtimes).name

// Codex as the server defines it once its driver lands (CODEX-DESIGN.md): a CLI on this computer with its own login, no commands of its
// own, no Pi extensions, and a lowest supported version.
export const codexDefinition: Schemas['RuntimeDefinition'] = {
  id: 'codex',
  name: 'Codex',
  providers: ['codex-cli'],
  isDefault: false,
  capabilities: { accounts: 'local', realHome: false, nativeCommands: false, pluginExtensions: false, bundled: false },
  minVersion: '0.167.0',
  setup: { install: 'npm install -g @openai/codex', login: 'codex login', docsUrl: 'https://developers.openai.com/codex/cli' },
}

// GET /runtime-definitions with Codex as well.
export const withCodex = (): Schemas['RuntimeDefinitionList'] => ({ runtimes: [...runtimeDefinitions().runtimes, codexDefinition] })

// The status of Codex on this computer: installed, logged in with a ChatGPT account, unless overridden.
export function codexStatus(overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return { id: 'codex', available: true, version: '0.170.0', binaryPath: '/usr/local/bin/codex', unavailableReason: null, login: { state: 'logged_in', authMethod: 'chatgpt', subscriptionType: null }, checkedAt, ...overrides }
}

// Grok and Antigravity as the server defines them (ACP-DESIGN.md): CLIs on this computer with their own login and the real home, no
// commands of their own, no Pi extensions.
const acp = (id: 'grok' | 'antigravity', name: string, binary: string): Schemas['RuntimeDefinition'] => ({
  id,
  name,
  providers: [`${id}-cli`],
  isDefault: false,
  capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false },
  minVersion: null,
  setup: { install: null, login: binary, docsUrl: null },
})
export const grokDefinition = acp('grok', 'Grok', 'grok')
export const antigravityDefinition = acp('antigravity', 'Antigravity', 'agy')

// GET /runtime-definitions with every runtime the console knows.
export const withAllRuntimes = (): Schemas['RuntimeDefinitionList'] => ({ runtimes: [...withCodex().runtimes, grokDefinition, antigravityDefinition] })

export function acpStatus(id: 'grok' | 'antigravity', overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return { id, available: true, version: id === 'grok' ? '1.0.88' : '1.5.14', binaryPath: id === 'grok' ? '/usr/local/bin/grok' : '/usr/local/bin/agy', unavailableReason: null, login: { state: 'logged_in', authMethod: null, subscriptionType: null }, checkedAt, ...overrides }
}
