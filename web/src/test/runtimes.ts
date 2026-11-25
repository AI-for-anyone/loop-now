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
      { id: 'pi', name: 'Pi', providers: [], isDefault: true, capabilities: { accounts: 'managed', realHome: false, nativeCommands: true, pluginExtensions: true, bundled: true, stopEndsBackgroundTasks: false }, minVersion: null, setup: null, acpCommandId: null },
      { id: 'claude-code', name: 'Claude Code', providers: ['claude-code'], isDefault: false, capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false, stopEndsBackgroundTasks: false }, minVersion: null, setup: { install: 'curl -fsSL https://claude.ai/install.sh | bash', login: 'claude', docsUrl: 'https://docs.anthropic.com/en/docs/claude-code/setup' }, acpCommandId: null },
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
  capabilities: { accounts: 'local', realHome: false, nativeCommands: false, pluginExtensions: false, bundled: false, stopEndsBackgroundTasks: false },
  minVersion: '0.167.0',
  acpCommandId: null,
  setup: { install: 'npm install -g @openai/codex', login: 'codex login', docsUrl: 'https://developers.openai.com/codex/cli' },
}

// GET /runtime-definitions with Codex as well.
export const withCodex = (): Schemas['RuntimeDefinitionList'] => ({ runtimes: [...runtimeDefinitions().runtimes, codexDefinition] })

// The status of Codex on this computer: installed, logged in with a ChatGPT account, unless overridden.
export function codexStatus(overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return { id: 'codex', available: true, version: '0.170.0', binaryPath: '/usr/local/bin/codex', unavailableReason: null, login: { state: 'logged_in', authMethod: 'chatgpt', subscriptionType: null }, checkedAt, ...overrides }
}

// Grok and Antigravity as the server defines them (ACP-DESIGN.md): CLIs on this computer with their own login and the real home, no
// commands of their own, no Pi extensions. Stopping a Grok reply ends its process, and with it the background tasks.
const acp = (id: 'grok' | 'antigravity', name: string, binary: string, stopEndsBackgroundTasks = false): Schemas['RuntimeDefinition'] => ({
  id,
  name,
  providers: [`${id}-cli`],
  isDefault: false,
  capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false, stopEndsBackgroundTasks },
  minVersion: null,
  setup: { install: null, login: binary, docsUrl: null },
  acpCommandId: null,
})
export const grokDefinition = acp('grok', 'Grok', 'grok', true)
export const antigravityDefinition = acp('antigravity', 'Antigravity', 'agy')

// GET /runtime-definitions with every runtime the console knows.
export const withAllRuntimes = (): Schemas['RuntimeDefinitionList'] => ({ runtimes: [...withCodex().runtimes, grokDefinition, antigravityDefinition] })

export function acpStatus(id: 'grok' | 'antigravity', overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return { id, available: true, version: id === 'grok' ? '1.0.88' : '1.5.14', binaryPath: id === 'grok' ? '/usr/local/bin/grok' : '/usr/local/bin/agy', unavailableReason: null, login: { state: 'logged_in', authMethod: null, subscriptionType: null }, checkedAt, ...overrides }
}

// A local ACP command a person added (contract AcpCommand): its runtime and model provider are both acp-<command id>, it uses the
// program's own login, and its one model is the program's default, with no thinking level to choose and no images.
export const acpCommandId = '7a0c4e2b-5d1f-4e8a-9b3c-2f6d8e1a4c05'
export const acpRuntimeId = `acp-${acpCommandId}`
export const acpCommandDefinition = (commandId = acpCommandId, name = 'Hermes'): Schemas['RuntimeDefinition'] => ({
  id: `acp-${commandId}`,
  name,
  providers: [`acp-${commandId}`],
  isDefault: false,
  capabilities: { accounts: 'local', realHome: true, nativeCommands: false, pluginExtensions: false, bundled: false, stopEndsBackgroundTasks: false },
  minVersion: null,
  setup: null,
  acpCommandId: commandId,
})
export function acpCommandStatus(commandId = acpCommandId, overrides: Partial<Schemas['RuntimeStatus']> = {}): Schemas['RuntimeStatus'] {
  return { id: `acp-${commandId}`, available: true, version: null, binaryPath: '/usr/local/bin/hermes', unavailableReason: null, login: { state: 'unknown', authMethod: null, subscriptionType: null }, checkedAt, ...overrides }
}
export const acpDefaultModel: Schemas['Model'] = { id: 'default', name: 'Default model', reasoning: false, thinkingLevels: ['off'], defaultThinkingLevel: 'off', contextWindow: 0, maxTokens: 0, input: ['text'] }
export function acpCommand(overrides: Partial<Schemas['AcpCommand']> = {}): Schemas['AcpCommand'] {
  const id = overrides.id ?? acpCommandId
  return { id, runtime: `acp-${id}`, name: 'Hermes', command: 'hermes', args: ['acp', '--stdio'], envNames: ['HERMES_API_KEY'], createdAt: checkedAt, updatedAt: checkedAt, ...overrides }
}
