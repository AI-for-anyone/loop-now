import type { MermaidConfig } from 'mermaid'
import { getThemeSnapshot } from '@/theme/store'
import type { ResolvedTheme, SkinId } from '@/theme/skins'

export interface MermaidAppearance {
  skin: SkinId
  resolvedTheme: ResolvedTheme
}

export const mermaidConfig: MermaidConfig = {
  startOnLoad: false,
  securityLevel: 'strict',
  htmlLabels: false,
  suppressErrorRendering: true,
  maxTextSize: 50_000,
  maxEdges: 500,
  secure: ['secure', 'startOnLoad', 'securityLevel', 'htmlLabels', 'suppressErrorRendering', 'maxTextSize', 'maxEdges', 'dompurifyConfig', 'flowchart', 'sequence', 'themeCSS'],
}

export function mermaidConfigFor(skin: SkinId, resolvedTheme: ResolvedTheme): MermaidConfig {
  if (resolvedTheme === 'dark') {
    return {
      ...mermaidConfig,
      theme: 'dark',
      themeVariables: {
        darkMode: true,
        background: '#0D0D0D',
        primaryColor: '#1A1A1A',
        primaryTextColor: '#EDEDED',
        primaryBorderColor: '#404040',
        lineColor: '#A3A3A3',
        secondaryColor: '#141414',
        tertiaryColor: '#111111',
        mainBkg: '#141414',
        nodeBorder: '#525252',
        clusterBkg: '#0A0A0A',
        titleColor: '#EDEDED',
        edgeLabelBackground: '#111111',
      },
    }
  }
  if (skin === 'mono') {
    return {
      ...mermaidConfig,
      theme: 'neutral',
      themeVariables: {
        darkMode: false,
        background: '#FAFAFA',
        primaryColor: '#F4F4F4',
        primaryTextColor: '#111111',
        primaryBorderColor: '#D4D4D4',
        lineColor: '#525252',
        secondaryColor: '#FFFFFF',
        tertiaryColor: '#FAFAFA',
        mainBkg: '#FFFFFF',
        nodeBorder: '#A3A3A3',
        clusterBkg: '#FAFAFA',
        titleColor: '#111111',
        edgeLabelBackground: '#FFFFFF',
      },
    }
  }
  return {
    ...mermaidConfig,
    theme: 'default',
  }
}

let engine: Promise<typeof import('mermaid')['default']> | undefined
let activeThemeKey: string | null = null
let nextId = 0

function load() {
  engine ??= import('mermaid').then(({ default: mermaid }) => mermaid)
  return engine
}

export async function renderMermaid(source: string, appearance?: MermaidAppearance): Promise<string> {
  if (source.length > mermaidConfig.maxTextSize!) throw new Error('Diagram exceeds maxTextSize')
  const { skin, resolvedTheme } = appearance ?? getThemeSnapshot()
  const mermaid = await load()
  const themeKey = `${skin}:${resolvedTheme}`
  if (activeThemeKey !== themeKey) {
    mermaid.initialize(mermaidConfigFor(skin, resolvedTheme))
    activeThemeKey = themeKey
  }
  // Each queued render owns its temporary DOM, even if an earlier component has unmounted.
  const { svg } = await mermaid.render(`loop-mermaid-${++nextId}`, source)
  // Deliberately do not call bindFunctions: diagrams cannot install click callbacks.
  return svg
}

// Reset cached initialization state between unit tests.
export function resetMermaidForTest(): void {
  activeThemeKey = null
}
