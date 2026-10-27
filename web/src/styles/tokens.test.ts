import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// 解析 index.css 中的令牌与代码高亮配色，按 WCAG 2 计算文字与底色的对比度，逐项不低于 4.5:1。
// 测试在 web/ 目录下运行。
const css = readFileSync(resolve(process.cwd(), 'src/index.css'), 'utf8')
const root = css.slice(css.indexOf(':root {'), css.indexOf('}', css.indexOf(':root {')))
const tokens = Object.fromEntries([...root.matchAll(/--([a-z0-9-]+):\s*(#[0-9A-Fa-f]{6});/g)].map(([, name, hex]) => [name, hex]))

function token(name: string): string {
  const value = name.startsWith('#') ? name : tokens[name]
  if (!value) throw new Error(`index.css 中没有令牌 --${name}`)
  return value
}

function luminance(hex: string): number {
  const channel = (offset: number) => {
    const c = parseInt(hex.slice(offset, offset + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5)
}

function contrast(foreground: string, background: string): number {
  const [light, dark] = [luminance(token(foreground)), luminance(token(background))].sort((a, b) => b - a)
  return (light + 0.05) / (dark + 0.05)
}

const WHITE = '#FFFFFF'

const pairs: [string, string, string][] = [
  ['正文', 'ink', 'paper'],
  ['次要文字', 'ink-700', 'paper'],
  ['元信息（页面）', 'ink-500', 'paper'],
  ['元信息（卡片）', 'ink-500', 'paper-raised'],
  ['元信息（白底条目）', 'ink-500', WHITE],
  ['元信息（浅蓝底）', 'ink-500', 'tint-blue'],
  ['链接', 'riso-blue', 'paper'],
  ['胶囊：中性、待写入', 'ink', 'tint-blue'],
  ['胶囊：成功、空闲、已设置', 'green-text', 'tint-green'],
  ['胶囊：失败、会话丢失、无法连接', 'red-text', 'tint-red'],
  ['胶囊：忙、回复中', 'ink', 'riso-pink'],
  ['胶囊：风险提示、部分依赖不可用', 'ink', 'tint-yellow'],
  ['印章：已打断', 'pink-text', 'paper-raised'],
  ['印章：已打断（页面）', 'pink-text', 'paper'],
  ['印章：服务端重启中断', 'ink-700', 'paper-raised'],
  ['印章：失败', 'red-text', 'paper-raised'],
  ['贴纸：空闲', 'green-text', 'tint-green'],
  ['贴纸：正忙', 'ink', 'riso-pink'],
  ['贴纸：会话丢失', 'red-text', 'tint-red'],
  ['顶栏：文字与已连接', WHITE, 'riso-blue'],
  ['顶栏：部分依赖不可用', 'ink', 'tint-yellow'],
  ['顶栏：无法连接', 'red-text', 'tint-red'],
  ['忙碌提示', 'ink', 'paper'],
  ['便签', 'ink', 'riso-yellow'],
  ['出错的输入框', 'ink', 'error-field'],
  ['出错说明', 'red-text', 'paper-raised'],
  ['按钮 default', WHITE, 'riso-blue'],
  ['按钮 interrupt', 'ink', 'riso-pink'],
  ['按钮 secondary', 'ink', 'tint-blue'],
  ['按钮 ghost', 'ink', 'paper-raised'],
  ['按钮 destructive', WHITE, 'red'],
  ['按钮 destructive-link', 'red-text', 'paper-raised'],
  ['按钮 ink', 'riso-yellow', 'ink'],
  ['用户消息气泡', 'ink', 'tint-pink'],
  // add-observability：分段控件、概览与运行记录。
  ['分段控件：未选中', 'ink-700', 'tint-blue'],
  ['分段控件：选中', 'ink', 'riso-yellow'],
  ['概览汇总卡：回复中', 'ink-700', 'tint-pink-2'],
  ['概览汇总卡：空闲', 'ink-700', 'tint-green'],
  ['概览汇总卡：会话丢失', 'ink-700', 'tint-red'],
  ['概览汇总卡：排队中', 'ink-700', 'tint-blue'],
  ['概览：大号合计与汇总数', 'riso-blue', 'paper-raised'],
  ['概览：汇总数（空闲）', 'green-text', 'tint-green'],
  ['概览：汇总数（会话丢失）', 'red-text', 'tint-red'],
  ['概览：汇总数（排队中）', 'riso-blue', 'tint-blue'],
  ['概览：出错横幅正文', 'ink', 'tint-red'],
  ['概览：用量明细的悬停提示', WHITE, 'ink'],
  ['概览：运行状态实时更新中', 'green-text', 'tint-green'],
  ['运行记录：步骤说明', 'ink-700', WHITE],
  ['运行记录：失败的用时', 'red-text', WHITE],
  ['运行记录：结局（被打断）', 'pink-text', 'tint-pink-2'],
  ['运行记录：结局（服务端重启中断）', 'ink-700', 'tint-blue'],
  ['定位：突出显示的回复', 'ink', 'tint-yellow'],
  ['定位：突出显示的回复中的元信息', 'ink-500', 'tint-yellow'],
]

// 代码块是孔版蓝底；高亮颜色取自 index.css 中 .hljs* 的 color。
const hljsColors = [...css.slice(css.indexOf('.hljs {')).matchAll(/color:\s*(#[0-9A-Fa-f]{6})/g)].map(([, hex]) => hex)

describe('设计令牌的对比度', () => {
  it.each(pairs)('%s：不低于 4.5:1', (_name, foreground, background) => {
    expect(contrast(foreground, background)).toBeGreaterThanOrEqual(4.5)
  })

  it('代码块中的文字与各高亮颜色在孔版蓝上都不低于 4.5:1', () => {
    expect(hljsColors.length).toBeGreaterThan(5)
    for (const color of hljsColors) expect(contrast(color, 'riso-blue'), color).toBeGreaterThanOrEqual(4.5)
  })

  it('荧光粉、荧光黄在纸上达不到正文对比度，所以不能作文字色', () => {
    expect(contrast('riso-pink', 'paper')).toBeLessThan(4.5)
    expect(contrast('riso-yellow', 'paper')).toBeLessThan(4.5)
  })
})
