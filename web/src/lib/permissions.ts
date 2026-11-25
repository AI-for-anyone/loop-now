import type { Schemas } from '@/api/client'

type Mode = Schemas['PermissionMode']
type Definition = Pick<Schemas['RuntimeDefinition'], 'capabilities'>

// The four modes (as T3), in the order they are offered: most careful first.
export const PERMISSION_MODES = ['supervised', 'accept_edits', 'auto', 'full'] as const satisfies readonly Mode[]

// The mode a runtime actually runs an Agent in: the Agent's own when the runtime has it, otherwise Supervised (never Full access).
export function effectiveMode(mode: Mode, runtime: Definition | undefined): Mode {
  if (!runtime || runtime.capabilities.permissionModes.includes(mode)) return mode
  return 'supervised'
}

// Whether a runtime can run an Agent as it is set up: not one that cannot work in the Agent's own folder, and not one that would have to
// drop its permission mode to Full access (a runtime with no Supervised, such as Pi, runs only on Full access).
export function canRunAgent(agent: Pick<Schemas['Agent'], 'workingDirectory' | 'permissionMode'>, runtime: Definition): boolean {
  if (agent.workingDirectory && !runtime.capabilities.workingDirectory) return false
  return agent.permissionMode === 'full' || runtime.capabilities.permissionModes.includes('supervised')
}
