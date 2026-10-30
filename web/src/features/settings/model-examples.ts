export type ModelExampleId = 'openai-compatible' | 'reasoning-vision' | 'builtin-override' | 'multiple-models'

export interface ModelExample {
  id: ModelExampleId
  providers: Record<string, unknown>
}

const openAIModel = {
  id: 'your-model-id',
  name: 'Your model',
  reasoning: false,
  input: ['text'],
  contextWindow: 128000,
  maxTokens: 8192,
}

export const MODEL_EXAMPLES: ModelExample[] = [
  {
    id: 'openai-compatible',
    providers: {
      'my-openai': {
        baseUrl: 'https://api.example.com/v1',
        api: 'openai-completions',
        apiKey: '$MY_OPENAI_API_KEY',
        models: [openAIModel],
      },
    },
  },
  {
    id: 'reasoning-vision',
    providers: {
      'my-reasoning-model': {
        baseUrl: 'https://api.example.com/v1',
        api: 'openai-completions',
        apiKey: '$MY_REASONING_API_KEY',
        models: [{
          id: 'reasoning-vision-model',
          name: 'Reasoning vision model',
          reasoning: true,
          input: ['text', 'image'],
          contextWindow: 256000,
          maxTokens: 32768,
          thinkingLevelMap: {
            off: null,
            minimal: null,
            low: 'low',
            medium: 'medium',
            high: 'high',
            xhigh: null,
            max: null,
          },
        }],
      },
    },
  },
  {
    id: 'builtin-override',
    providers: {
      openai: {
        baseUrl: 'https://openai-proxy.example.com/v1',
        apiKey: '$MY_OPENAI_PROXY_KEY',
      },
    },
  },
  {
    id: 'multiple-models',
    providers: {
      'my-models': {
        baseUrl: 'https://api.example.com/v1',
        api: 'openai-completions',
        apiKey: '$MY_MODELS_API_KEY',
        models: [
          openAIModel,
          { ...openAIModel, id: 'your-vision-model-id', name: 'Your vision model', input: ['text', 'image'] },
        ],
      },
    },
  },
]

export function exampleJSON(example: ModelExample): string {
  return JSON.stringify({ providers: example.providers }, null, 2)
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export type MergeExampleResult =
  | { kind: 'merged'; text: string }
  | { kind: 'conflict'; providers: string[] }
  | { kind: 'invalid'; reason: 'syntax' | 'providers' }

export function mergeExample(text: string, example: ModelExample, overwrite = false): MergeExampleResult {
  let root: unknown
  try {
    root = JSON.parse(text)
  } catch {
    return { kind: 'invalid', reason: 'syntax' }
  }
  if (!isObject(root) || !isObject(root.providers)) return { kind: 'invalid', reason: 'providers' }
  const conflicts = Object.keys(example.providers).filter(id => Object.hasOwn(root.providers as Record<string, unknown>, id))
  if (conflicts.length > 0 && !overwrite) return { kind: 'conflict', providers: conflicts }
  const merged = { ...root, providers: { ...root.providers, ...example.providers } }
  return { kind: 'merged', text: `${JSON.stringify(merged, null, 2)}\n` }
}
