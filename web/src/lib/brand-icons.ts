import ampColor from '@/assets/brand-icons/amp-color.svg'
import antigravityColor from '@/assets/brand-icons/antigravity-color.svg'
import claudeColor from '@/assets/brand-icons/claude-color.svg'
import claudeCodeColor from '@/assets/brand-icons/claudecode-color.svg'
import clineMono from '@/assets/brand-icons/cline.svg'
import codexColor from '@/assets/brand-icons/codex-color.svg'
import cursorMono from '@/assets/brand-icons/cursor.svg'
import deepseekColor from '@/assets/brand-icons/deepseek-color.svg'
import geminiColor from '@/assets/brand-icons/gemini-color.svg'
import geminiCliColor from '@/assets/brand-icons/geminicli-color.svg'
import copilotMono from '@/assets/brand-icons/githubcopilot.svg'
import gooseMono from '@/assets/brand-icons/goose.svg'
import grokMono from '@/assets/brand-icons/grok.svg'
import kimiColor from '@/assets/brand-icons/kimi-color.svg'
import kiroColor from '@/assets/brand-icons/kiro-color.svg'
import minimaxColor from '@/assets/brand-icons/minimax-color.svg'
import mistralColor from '@/assets/brand-icons/mistral-color.svg'
import ollamaMono from '@/assets/brand-icons/ollama.svg'
import openaiMono from '@/assets/brand-icons/openai.svg'
import opencodeMono from '@/assets/brand-icons/opencode.svg'
import openrouterMono from '@/assets/brand-icons/openrouter.svg'
import qwenColor from '@/assets/brand-icons/qwen-color.svg'
import roocodeMono from '@/assets/brand-icons/roocode.svg'
import traeColor from '@/assets/brand-icons/trae-color.svg'
import windsurfMono from '@/assets/brand-icons/windsurf.svg'
import zhipuColor from '@/assets/brand-icons/zhipu-color.svg'

export type BrandIconKey =
  | 'pi'
  | 'acp'
  | 'claudecode'
  | 'claude'
  | 'codex'
  | 'openai'
  | 'antigravity'
  | 'geminicli'
  | 'gemini'
  | 'grok'
  | 'opencode'
  | 'cursor'
  | 'copilot'
  | 'amp'
  | 'cline'
  | 'goose'
  | 'kiro'
  | 'windsurf'
  | 'roocode'
  | 'trae'
  | 'deepseek'
  | 'qwen'
  | 'kimi'
  | 'zhipu'
  | 'minimax'
  | 'mistral'
  | 'ollama'
  | 'openrouter'

export interface BrandIconAsset {
  src: string
  // Monochrome marks use CSS mask-image with currentColor so they invert automatically across light and dark themes.
  mono: boolean
}

// Vendored official SVGs from @lobehub/icons-static-svg (MIT). 'pi' and 'acp' are rendered as crisp inline geometric strokes.
export const BRAND_ICON_ASSETS: Partial<Record<BrandIconKey, BrandIconAsset>> = {
  claudecode: { src: claudeCodeColor, mono: false },
  claude: { src: claudeColor, mono: false },
  codex: { src: codexColor, mono: false },
  openai: { src: openaiMono, mono: true },
  antigravity: { src: antigravityColor, mono: false },
  geminicli: { src: geminiCliColor, mono: false },
  gemini: { src: geminiColor, mono: false },
  grok: { src: grokMono, mono: true },
  opencode: { src: opencodeMono, mono: true },
  cursor: { src: cursorMono, mono: true },
  copilot: { src: copilotMono, mono: true },
  amp: { src: ampColor, mono: false },
  cline: { src: clineMono, mono: true },
  goose: { src: gooseMono, mono: true },
  kiro: { src: kiroColor, mono: false },
  windsurf: { src: windsurfMono, mono: true },
  roocode: { src: roocodeMono, mono: true },
  trae: { src: traeColor, mono: false },
  deepseek: { src: deepseekColor, mono: false },
  qwen: { src: qwenColor, mono: false },
  kimi: { src: kimiColor, mono: false },
  zhipu: { src: zhipuColor, mono: false },
  minimax: { src: minimaxColor, mono: false },
  mistral: { src: mistralColor, mono: false },
  ollama: { src: ollamaMono, mono: true },
  openrouter: { src: openrouterMono, mono: true },
}

const ACP_NAME_MATCHERS: readonly [RegExp, BrandIconKey][] = [
  [/claude/i, 'claudecode'],
  [/codex/i, 'codex'],
  [/antigravity|\bagy\b/i, 'antigravity'],
  [/gemini/i, 'geminicli'],
  [/opencode/i, 'opencode'],
  [/cursor/i, 'cursor'],
  [/copilot/i, 'copilot'],
  [/\bamp\b/i, 'amp'],
  [/cline/i, 'cline'],
  [/goose/i, 'goose'],
  [/kiro/i, 'kiro'],
  [/windsurf/i, 'windsurf'],
  [/roo\s*code|\broocode\b/i, 'roocode'],
  [/trae/i, 'trae'],
  [/grok|xai/i, 'grok'],
]

// Infer a mainstream coding-harness logo for a user-added ACP command from its display name.
export function acpBrandIconByName(name: string): BrandIconKey {
  for (const [pattern, key] of ACP_NAME_MATCHERS) {
    if (pattern.test(name)) return key
  }
  return 'acp'
}

const PROVIDER_MATCHERS: readonly [RegExp, BrandIconKey][] = [
  [/anthropic|claude/i, 'claude'],
  [/openai|codex|gpt/i, 'openai'],
  [/google|gemini|vertex/i, 'gemini'],
  [/deepseek/i, 'deepseek'],
  [/qwen|dashscope|alibaba|tongyi/i, 'qwen'],
  [/kimi|moonshot/i, 'kimi'],
  [/zhipu|glm|bigmodel/i, 'zhipu'],
  [/minimax/i, 'minimax'],
  [/mistral/i, 'mistral'],
  [/xai|grok/i, 'grok'],
  [/ollama/i, 'ollama'],
  [/openrouter/i, 'openrouter'],
]

// Match a model provider id (e.g. "anthropic", "openai-codex", "google", "deepseek") to its official vendor logo.
export function providerBrandIcon(providerId: string | null | undefined): BrandIconKey | null {
  if (!providerId) return null
  for (const [pattern, key] of PROVIDER_MATCHERS) {
    if (pattern.test(providerId)) return key
  }
  return null
}
