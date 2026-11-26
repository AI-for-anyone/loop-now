import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { acpCommandDefinition, antigravityDefinition, codexDefinition, grokDefinition, runtimeDefinitions } from '@/test/runtimes'
import antigravityFile from '@/assets/runtime-logos/antigravity.svg'
import claudeFile from '@/assets/runtime-logos/claude.svg'
import grokFile from '@/assets/runtime-logos/grok.svg'
import openaiFile from '@/assets/runtime-logos/openai.svg'
import piFile from '@/assets/runtime-logos/pi.svg'
import { RuntimeGlyph, RuntimeMark } from './RuntimeMark'

const [pi, claudeCode] = runtimeDefinitions().runtimes
// Each runtime, the logo it is marked with, and the official file of that logo.
const builtIn: [string, Schemas['RuntimeDefinition'], string, string][] = [
  [claudeCode.name, claudeCode, 'claude', claudeFile],
  [codexDefinition.name, codexDefinition, 'openai', openaiFile],
  [grokDefinition.name, grokDefinition, 'grok', grokFile],
  [antigravityDefinition.name, antigravityDefinition, 'antigravity', antigravityFile],
  [pi.name, pi, 'pi', piFile],
]
const unknown = { ...claudeCode, id: 'acme', name: 'Acme CLI' } as unknown as Schemas['RuntimeDefinition']
const logoOf = (container: HTMLElement) => container.querySelector('img')

describe('runtime marks are the products’ own logos', () => {
  it.each(builtIn)('%s shows its own logo file', (_name, definition, logo, file) => {
    const { container } = render(<RuntimeMark runtime={definition} />)
    const image = logoOf(container)!
    expect(image).toHaveAttribute('data-logo', logo)
    expect(image).toHaveAttribute('src', file)
    // The mark is decoration beside the runtime's written name.
    expect(image).toHaveAttribute('alt', '')
    expect(container.querySelector('svg')).toBeNull()
  })
  it('no two runtimes share a logo', () => {
    const files = builtIn.map(([, definition]) => logoOf(render(<RuntimeMark runtime={definition} />).container)!.getAttribute('src'))
    expect(new Set(files).size).toBe(builtIn.length)
  })
  it('a local ACP command a person added gets the neutral drawn mark with its initial, not a product logo', () => {
    const { container } = render(<RuntimeMark runtime={acpCommandDefinition()} />)
    expect(logoOf(container)).toBeNull()
    expect(container.querySelector('svg text')).toHaveTextContent('H')
  })
  it('a runtime id the console does not know falls back to the neutral mark without crashing', () => {
    const { container } = render(<><RuntimeMark runtime={unknown} /><RuntimeMark runtime={{ ...unknown, id: 'constructor' as Schemas['AgentRuntime'], name: 'x' }} /></>)
    expect(logoOf(container)).toBeNull()
    expect([...container.querySelectorAll('svg text')].map((text) => text.textContent)).toEqual(['A', 'X'])
  })
})

describe('the small glyph', () => {
  it.each(builtIn)('%s uses its logo small, without the mark’s clear-space frame', (_name, definition, logo, file) => {
    const { container } = render(<RuntimeGlyph runtime={definition} />)
    const image = container.firstElementChild!
    expect(image.tagName).toBe('IMG')
    expect(image).toHaveAttribute('data-logo', logo)
    expect(image).toHaveAttribute('src', file)
    expect(image).toHaveClass('size-3.5')
  })
  it('a runtime without a logo shows its initial in the text color', () => {
    const { container } = render(<RuntimeGlyph runtime={acpCommandDefinition()} />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('svg text')).toHaveTextContent('H')
    expect(container.querySelector('svg text')).toHaveAttribute('fill', 'currentColor')
  })
})
