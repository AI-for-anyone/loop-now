import type { MermaidConfig } from 'mermaid'

export const mermaidConfig: MermaidConfig = {
  startOnLoad: false,
  securityLevel: 'strict',
  htmlLabels: false,
  suppressErrorRendering: true,
  maxTextSize: 50_000,
  maxEdges: 500,
  secure: ['secure', 'startOnLoad', 'securityLevel', 'htmlLabels', 'suppressErrorRendering', 'maxTextSize', 'maxEdges', 'dompurifyConfig', 'flowchart', 'sequence', 'themeCSS'],
}

let engine: Promise<typeof import('mermaid')['default']> | undefined
let nextId = 0
function load() {
  engine ??= import('mermaid').then(({ default: mermaid }) => {
    mermaid.initialize(mermaidConfig)
    return mermaid
  })
  return engine
}

export async function renderMermaid(source: string): Promise<string> {
  if (source.length > mermaidConfig.maxTextSize!) throw new Error('Diagram exceeds maxTextSize')
  const mermaid = await load()
  // Each queued render owns its temporary DOM, even if an earlier component has unmounted.
  const { svg } = await mermaid.render(`loop-mermaid-${++nextId}`, source)
  // Deliberately do not call bindFunctions: diagrams cannot install click callbacks.
  return svg
}
