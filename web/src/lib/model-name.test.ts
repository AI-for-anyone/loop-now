import { describe, expect, it } from 'vitest'
import { shortModelName } from './model-name'

describe('shortModelName', () => {
  it('shortens Anthropic Claude models to concise tier and version labels', () => {
    expect(shortModelName({ id: 'claude-sonnet-4-5', name: 'Claude Sonnet 4.5' })).toBe('Sonnet 4.5')
    expect(shortModelName({ id: 'claude-sonnet-4-5-20250929', name: 'Claude Sonnet 4.5' })).toBe('Sonnet 4.5')
    expect(shortModelName({ id: 'claude-opus-4-1', name: 'Claude Opus 4.1' })).toBe('Opus 4.1')
    expect(shortModelName({ id: 'haiku', name: 'Claude Haiku (latest)' })).toBe('Haiku')
    expect(shortModelName({ id: 'claude-3-7-sonnet-20250219', name: 'Claude 3.7 Sonnet' })).toBe('Sonnet 3.7')
  })

  it('shortens OpenAI, Google, DeepSeek, Qwen, Kimi, GLM, and Grok models cleanly', () => {
    expect(shortModelName('gpt-5.4-codex')).toBe('GPT-5.4')
    expect(shortModelName('gpt-5-codex-mini')).toBe('GPT-5 Mini')
    expect(shortModelName('o4-mini')).toBe('o4-mini')
    expect(shortModelName('gemini-2.5-pro-preview-05-06')).toBe('Gemini 2.5 Pro')
    expect(shortModelName('deepseek-reasoner')).toBe('DeepSeek R1')
    expect(shortModelName('qwen3-coder-plus')).toBe('Qwen3 Coder')
    expect(shortModelName('kimi-k2-0711')).toBe('Kimi K2')
    expect(shortModelName('glm-4.7')).toBe('GLM-4.7')
    expect(shortModelName('grok-3-mini')).toBe('Grok 3 Mini')
  })

  it('falls back cleanly to trimmed display name or leaf slug for custom models', () => {
    expect(shortModelName({ id: 'custom/my-model-20260101', name: 'My Custom Model (latest)' })).toBe('My Custom Model')
    expect(shortModelName('provider/custom-v2')).toBe('custom-v2')
  })
})
