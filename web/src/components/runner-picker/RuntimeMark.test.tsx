import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import antigravityRaw from '@/assets/runtime-logos/antigravity.svg?raw'
import claudeRaw from '@/assets/runtime-logos/claude.svg?raw'
import grokRaw from '@/assets/runtime-logos/grok.svg?raw'
import openaiRaw from '@/assets/runtime-logos/openai.svg?raw'
import piRaw from '@/assets/runtime-logos/pi.svg?raw'
import { acpCommandDefinition, antigravityDefinition, codexDefinition, grokDefinition, runtimeDefinitions } from '@/test/runtimes'
import { RuntimeGlyph, RuntimeMark } from './RuntimeMark'

const [pi, claudeCode] = runtimeDefinitions().runtimes
// Each built-in runtime, its logo key, and the official SVG source file it draws from.
const builtIn: [string, Schemas['RuntimeDefinition'], string, string][] = [
  [claudeCode.name, claudeCode, 'claude', claudeRaw],
  [codexDefinition.name, codexDefinition, 'openai', openaiRaw],
  [grokDefinition.name, grokDefinition, 'grok', grokRaw],
  [antigravityDefinition.name, antigravityDefinition, 'antigravity', antigravityRaw],
  [pi.name, pi, 'pi', piRaw],
]
const unknown = { ...claudeCode, id: 'acme', name: 'Acme CLI' } as unknown as Schemas['RuntimeDefinition']

const pathsIn = (raw: string) => [...raw.matchAll(/<path\b[^>]*\bd="([^"]+)"/g)].map((match) => match[1])

// Every painted color in the rendered mark (fill, stroke, or inline style).
function colorsOf(root: Element): Set<string> {
  const colors = new Set<string>()
  for (const element of [root, ...root.querySelectorAll('*')]) {
    for (const attr of ['fill', 'stroke', 'color']) {
      const value = element.getAttribute(attr)
      if (value && value !== 'none') colors.add(value)
    }
    const style = element.getAttribute('style')
    if (style && /color|fill|stroke|background/i.test(style)) colors.add(style)
  }
  return colors
}

function expectInkFrame(svg: Element) {
  expect(svg.tagName.toLowerCase()).toBe('svg')
  expect(svg).toHaveAttribute('viewBox', '0 0 32 32')
  expect(svg).toHaveAttribute('aria-hidden', 'true')
  const frame = svg.querySelector(':scope > rect')
  expect(frame).not.toBeNull()
  expect(frame).toHaveAttribute('x', '0')
  expect(frame).toHaveAttribute('y', '0')
  expect(frame).toHaveAttribute('width', '32')
  expect(frame).toHaveAttribute('height', '32')
  expect(Number(frame!.getAttribute('rx'))).toBeGreaterThan(0)
  expect(frame).toHaveAttribute('fill', 'var(--ink)')
  expect(svg.querySelector('img, filter, mask, defs')).toBeNull()
  expect([...colorsOf(svg)].sort()).toEqual(['var(--ink)', 'var(--paper-raised)'])
}

describe('runtime marks share one ink frame with each product’s one-color shape inside', () => {
  it.each(builtIn)('%s renders the ink frame and its official shape in paper color, with no other colors', (_name, definition, logo, raw) => {
    const { container } = render(<RuntimeMark runtime={definition} />)
    const svg = container.firstElementChild!
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    expectInkFrame(svg)
    expect(svg).toHaveAttribute('data-logo', logo)
    const paths = [...svg.querySelectorAll('path')]
    expect(paths.map((path) => path.getAttribute('d'))).toEqual(pathsIn(raw))
    for (const path of paths) expect(path).toHaveAttribute('fill', 'var(--paper-raised)')
    // The shape is transformed inside the frame so clear space stays around it.
    expect(svg.querySelector('g')?.getAttribute('transform')).toMatch(/^translate\(.+\) scale\(.+\)$/)
  })

  it('no two built-in runtimes share a shape', () => {
    const shapes = builtIn.map(([, definition]) =>
      [...render(<RuntimeMark runtime={definition} />).container.querySelectorAll('path')]
        .map((path) => path.getAttribute('d'))
        .join('|'),
    )
    expect(new Set(shapes).size).toBe(builtIn.length)
  })

  it.each(builtIn)('the %s asset keeps its source comment and holds only a one-color silhouette', (_name, _definition, _logo, raw) => {
    expect(raw.trimStart()).toMatch(/^<!--[\s\S]+?-->/)
    expect(raw).not.toMatch(/<(?:filter|mask|defs)\b/i)
    expect(raw).not.toMatch(/\b(?:fill|stroke)="(?!none|currentColor)[^"]+"/i)
    expect(pathsIn(raw).length).toBeGreaterThan(0)
  })

  it('a local ACP command a person added renders its initial in the same ink frame', () => {
    const { container } = render(<RuntimeMark runtime={acpCommandDefinition()} />)
    const svg = container.firstElementChild!
    expectInkFrame(svg)
    expect(svg).not.toHaveAttribute('data-logo')
    const text = svg.querySelector('text')
    expect(text).toHaveTextContent('H')
    expect(text).toHaveAttribute('fill', 'var(--paper-raised)')
  })

  it('a runtime id the console does not know falls back to its initial in the same ink frame without crashing', () => {
    const { container } = render(<><RuntimeMark runtime={unknown} /><RuntimeMark runtime={{ ...unknown, id: 'constructor' as Schemas['AgentRuntime'], name: 'x' }} /></>)
    const svgs = [...container.querySelectorAll('svg')]
    expect(svgs).toHaveLength(2)
    for (const svg of svgs) expectInkFrame(svg)
    expect(svgs.map((svg) => svg.querySelector('text')?.textContent)).toEqual(['A', 'X'])
  })
})

describe('the small variant (RuntimeGlyph)', () => {
  it.each(builtIn)('%s uses the same ink frame scaled small, not a bare logo', (_name, definition, logo, raw) => {
    const { container } = render(<RuntimeGlyph runtime={definition} />)
    const svg = container.firstElementChild!
    expectInkFrame(svg)
    expect(svg).toHaveAttribute('data-logo', logo)
    expect(svg).toHaveClass('size-4')
    expect([...svg.querySelectorAll('path')].map((path) => path.getAttribute('d'))).toEqual(pathsIn(raw))
  })

  it('a local ACP command uses the same ink frame scaled small with its initial', () => {
    const { container } = render(<RuntimeGlyph runtime={acpCommandDefinition()} />)
    const svg = container.firstElementChild!
    expectInkFrame(svg)
    expect(svg).toHaveClass('size-4')
    expect(svg.querySelector('text')).toHaveTextContent('H')
    expect(svg.querySelector('text')).toHaveAttribute('fill', 'var(--paper-raised)')
  })
})
