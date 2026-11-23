import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { runtimeDefinitions } from '@/test/runtimes'
import { RuntimeDifferences } from './RuntimeNotes'

const [pi, claudeCode] = runtimeDefinitions().runtimes
// Runtimes the contract does not have: only their capabilities matter here.
const runtime = (name: string, capabilities: Partial<Schemas['RuntimeCapabilities']>): Schemas['RuntimeDefinition'] => ({ ...claudeCode, name, capabilities: { ...claudeCode.capabilities, ...capabilities } })
const home = 'It runs with your home directory and logins on this computer: gh, git and other commands it runs use your own logins.'
const commands = 'It runs commands without asking first, the same as Pi Agents.'
const plugins = 'Plugins that only contain Pi extensions don\'t apply to it; plugin skills and MCP tools work.'
const piCommands = 'There are no /pi: commands.'
const items = (name: string) => within(screen.getByRole('region', { name: `Running on ${name}` })).getAllByRole('listitem').map((item) => item.textContent)

describe('how a runtime differs from Pi, from its capabilities', () => {
  it('lists one point per difference under the runtime\'s name; brief keeps only the home directory and commands', () => {
    const { rerender } = render(<RuntimeDifferences runtime={claudeCode} />)
    expect(items('Claude Code')).toEqual([home, commands, plugins, piCommands])
    rerender(<RuntimeDifferences runtime={claudeCode} brief />)
    expect(items('Claude Code')).toEqual([home, commands])
  })

  it('each point follows its own capability', () => {
    render(<>
      <RuntimeDifferences runtime={runtime('Home only', { pluginExtensions: true, nativeCommands: true })} />
      <RuntimeDifferences runtime={runtime('No extensions', { realHome: false, nativeCommands: true })} />
      <RuntimeDifferences runtime={runtime('No commands', { realHome: false, pluginExtensions: true })} />
    </>)
    expect(items('Home only')).toEqual([home, commands])
    expect(items('No extensions')).toEqual([plugins])
    expect(items('No commands')).toEqual([piCommands])
  })

  it('nothing for Pi, for a runtime whose differences brief leaves out, or before the definitions are read', () => {
    const { container } = render(<>
      <RuntimeDifferences runtime={pi} />
      <RuntimeDifferences runtime={runtime('No extensions', { realHome: false })} brief />
      <RuntimeDifferences runtime={undefined} />
    </>)
    expect(container).toBeEmptyDOMElement()
  })
})
