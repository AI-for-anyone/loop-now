import { readFileSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// Parse the skin token blocks and code-highlighting colors in mono.css and plate.css, compute WCAG 2
// contrast ratios between text and background, and require at least 4.5:1 for every pair.
const monoCss = readFileSync(resolve(process.cwd(), 'src/styles/skins/mono.css'), 'utf8')
const plateCss = readFileSync(resolve(process.cwd(), 'src/styles/skins/plate.css'), 'utf8')

function extractBlock(source: string, selector: string): string {
  const start = source.indexOf(selector)
  if (start < 0) throw new Error(`Selector "${selector}" not found`)
  const open = source.indexOf('{', start)
  const close = source.indexOf('}', open)
  return source.slice(open, close)
}

function parseHexTokens(block: string): Record<string, string> {
  return Object.fromEntries([...block.matchAll(/--([a-z0-9-]+):\s*(#[0-9A-Fa-f]{6});/g)].map(([, name, hex]) => [name, hex]))
}

function luminance(hex: string): number {
  const channel = (offset: number) => {
    const c = parseInt(hex.slice(offset, offset + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5)
}

function contrastFrom(tokens: Record<string, string>, foreground: string, background: string): number {
  const resolveHex = (name: string) => {
    const value = name.startsWith('#') ? name : tokens[name]
    if (!value) throw new Error(`Missing hex token --${name}`)
    return value
  }
  const [light, dark] = [luminance(resolveHex(foreground)), luminance(resolveHex(background))].sort((a, b) => b - a)
  return (light + 0.05) / (dark + 0.05)
}

const WHITE = '#FFFFFF'

const plateTokens = parseHexTokens(extractBlock(plateCss, ':root[data-skin="plate"] {'))
const monoLightTokens = parseHexTokens(extractBlock(monoCss, ':root,\n:root[data-skin="mono"] {'))
const monoDarkTokens = parseHexTokens(extractBlock(monoCss, ':root[data-skin="mono"][data-theme="dark"],'))

const platePairs: [string, string, string][] = [
  ['Body', 'ink', 'paper'],
  ['Secondary text', 'ink-700', 'paper'],
  ['Meta (page)', 'ink-500', 'paper'],
  ['Meta (card)', 'ink-500', 'paper-raised'],
  ['Meta (white item)', 'ink-500', WHITE],
  ['Meta (light blue background)', 'ink-500', 'tint-blue'],
  ['Link', 'riso-blue', 'paper'],
  ['Pill: neutral, pending write', 'ink', 'tint-blue'],
  ['Pill: success, idle, set', 'green-text', 'tint-green'],
  ['Pill: failed, unreachable', 'red-text', 'tint-red'],
  ['Pill: busy, replying', 'ink', 'riso-pink'],
  ['Pill: risk warning, database unavailable', 'ink', 'tint-yellow'],
  ['Stamp: interrupted', 'pink-text', 'paper-raised'],
  ['Stamp: interrupted (page)', 'pink-text', 'paper'],
  ['Stamp: cut off by a server restart', 'ink-700', 'paper-raised'],
  ['Stamp: failed', 'red-text', 'paper-raised'],
  ['Sticker: idle', 'green-text', 'tint-green'],
  ['Sticker: busy', 'ink', 'riso-pink'],
  ['Top bar: text and connected', WHITE, 'riso-blue'],
  ['Top bar: database unavailable', 'ink', 'tint-yellow'],
  ['Top bar: unreachable', 'red-text', 'tint-red'],
  ['Busy notice', 'ink', 'paper'],
  ['Sticky note', 'ink', 'riso-yellow'],
  ['Input with an error', 'ink', 'error-field'],
  ['Error text', 'red-text', 'paper-raised'],
  ['Button default', WHITE, 'riso-blue'],
  ['Button interrupt', 'ink', 'riso-pink'],
  ['Button secondary', 'ink', 'tint-blue'],
  ['Button ghost', 'ink', 'paper-raised'],
  ['Button destructive', WHITE, 'red'],
  ['Button destructive-link', 'red-text', 'paper-raised'],
  ['Button ink', 'riso-yellow', 'ink'],
  ['User message bubble', 'ink', 'tint-pink'],
  ['Segmented control: not selected', 'ink-700', 'tint-blue'],
  ['Segmented control: selected', 'ink', 'riso-yellow'],
  ['Overview summary tile: replying', 'ink-700', 'tint-pink-2'],
  ['Overview summary tile: idle', 'ink-700', 'tint-green'],
  ['Overview summary tile: queued', 'ink-700', 'tint-blue'],
  ['Overview: large total and summary counts', 'riso-blue', 'paper-raised'],
  ['Overview: summary count (idle)', 'green-text', 'tint-green'],
  ['Overview: summary count (queued)', 'riso-blue', 'tint-blue'],
  ['Overview: error banner body', 'ink', 'tint-red'],
  ['Overview: usage detail hover tooltip', WHITE, 'ink'],
  ['Overview: run status updating live', 'green-text', 'tint-green'],
  ['Run log: step description', 'ink-700', WHITE],
  ['Run log: duration of a failure', 'red-text', WHITE],
  ['Run log: outcome (interrupted)', 'pink-text', 'tint-pink-2'],
  ['Run log: outcome (cut off by a server restart)', 'ink-700', 'tint-blue'],
  ['Locate: highlighted reply', 'ink', 'tint-yellow'],
  ['Locate: meta in a highlighted reply', 'ink-500', 'tint-yellow'],
  ['Index tab: not selected', 'ink-700', 'paper-tab'],
  ['Index tab: current page', 'ink', 'riso-yellow'],
  ['Stub: small heading', 'ink-500', 'paper-raised'],
  ['Composer: key hints', 'ink-700', 'tint-yellow'],
  ['Composer: live connection OK', 'green-text', 'tint-yellow'],
  ['Composer: reconnecting', 'red-text', 'tint-yellow'],
]

const monoPairs: [string, string, string][] = [
  ['Body on canvas', 'ink', 'surface-canvas'],
  ['Secondary on canvas', 'ink-700', 'surface-canvas'],
  ['Meta on canvas', 'ink-500', 'surface-canvas'],
  ['Body on subtle surface', 'ink', 'surface-subtle'],
  ['Secondary on subtle surface', 'ink-700', 'surface-subtle'],
  ['Meta on subtle surface', 'ink-500', 'surface-subtle'],
  ['Body on elevated surface', 'ink', 'surface-elevated'],
  ['Meta on elevated surface', 'ink-500', 'surface-elevated'],
  ['Sidebar primary', 'sidebar-fg', 'sidebar-bg'],
  ['Sidebar muted', 'sidebar-muted', 'sidebar-bg'],
  ['Sidebar active row', 'sidebar-active-fg', 'sidebar-active-bg'],
  ['Primary CTA', 'cta-fg', 'cta-bg'],
  ['Tooltip text', 'tooltip-fg', 'tooltip-bg'],
  ['Tooltip muted', 'tooltip-muted', 'tooltip-bg'],
  ['Code text', 'code-fg', 'code-bg'],
  ['Code muted', 'code-muted', 'code-bg'],
  ['Status: success on tint', 'green-text', 'tint-green'],
  ['Status: error on tint', 'red-text', 'tint-red'],
  ['Status: interrupted on tint', 'pink-text', 'tint-pink-2'],
  ['Status: warning on tint', 'ink', 'tint-yellow'],
  ['Destructive CTA', WHITE, 'red'],
  ['User message block', 'ink', 'tint-pink'],
  ['Error text on elevated', 'red-text', 'surface-elevated'],
  ['Success text on elevated', 'green-text', 'surface-elevated'],
]

const plateHljsColors = [...plateCss.slice(plateCss.indexOf(':root[data-skin="plate"] .hljs {')).matchAll(/color:\s*(#[0-9A-Fa-f]{6})/g)].map(([, hex]) => hex)
const monoLightHljsSlice = monoCss.slice(
  monoCss.indexOf(':root:not([data-skin="plate"]) .hljs {'),
  monoCss.indexOf('/* Monochrome syntax highlighting (dark)'),
)
const monoLightHljsColors = [...monoLightHljsSlice.matchAll(/color:\s*(#[0-9A-Fa-f]{6})/g)].map(([, hex]) => hex)
const monoDarkHljsColors = [
  ...monoCss.slice(monoCss.indexOf('/* Monochrome syntax highlighting (dark)')).matchAll(/color:\s*(#[0-9A-Fa-f]{6})/g),
].map(([, hex]) => hex)

describe('Design token contrast — Plate skin', () => {
  it.each(platePairs)('%s: at least 4.5:1', (_name, foreground, background) => {
    expect(contrastFrom(plateTokens, foreground, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('code block text and every highlight color are at least 4.5:1 on riso blue', () => {
    expect(plateHljsColors.length).toBeGreaterThan(5)
    for (const color of plateHljsColors) expect(contrastFrom(plateTokens, color, 'riso-blue'), color).toBeGreaterThanOrEqual(4.5)
  })

  it('the paper grain is a tiled image under 12 KB', () => {
    const start = plateCss.indexOf(':root[data-skin="plate"] .riso-grain {')
    const rule = plateCss.slice(start, plateCss.indexOf('}', start))
    const file = /url\(['"]?([^'")]+)['"]?\)/.exec(rule)?.[1]
    expect(file).toMatch(/\.png$/)
    expect(rule).toMatch(/background-size:\s*128px 128px/)
    expect(statSync(resolve(process.cwd(), 'src/styles/skins', file!)).size).toBeLessThan(12 * 1024)
    expect(plateCss).not.toContain('feTurbulence')
  })

  it('fluorescent pink and yellow do not reach body text contrast on paper, so they cannot be text colors', () => {
    expect(contrastFrom(plateTokens, 'riso-pink', 'paper')).toBeLessThan(4.5)
    expect(contrastFrom(plateTokens, 'riso-yellow', 'paper')).toBeLessThan(4.5)
  })
})

describe('Design token contrast — Mono skin (light)', () => {
  it.each(monoPairs)('%s: at least 4.5:1', (_name, foreground, background) => {
    expect(contrastFrom(monoLightTokens, foreground, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('every light code highlight color reaches at least 4.5:1 on code background', () => {
    expect(monoLightHljsColors.length).toBeGreaterThan(5)
    for (const color of monoLightHljsColors) expect(contrastFrom(monoLightTokens, color, 'code-bg'), color).toBeGreaterThanOrEqual(4.5)
  })
})

describe('Design token contrast — Mono skin (dark)', () => {
  it.each(monoPairs)('%s: at least 4.5:1', (_name, foreground, background) => {
    expect(contrastFrom(monoDarkTokens, foreground, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('every dark code highlight color reaches at least 4.5:1 on code background', () => {
    expect(monoDarkHljsColors.length).toBeGreaterThan(5)
    for (const color of monoDarkHljsColors) expect(contrastFrom(monoDarkTokens, color, 'code-bg'), color).toBeGreaterThanOrEqual(4.5)
  })
})
