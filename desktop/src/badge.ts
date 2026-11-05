import { app, nativeImage, type BrowserWindow, type NativeImage } from 'electron'
import { badgeStyle, current, type PlatformId } from './platform'

// 未读数怎么显示：macOS 是 Dock 角标；Windows 没有 Dock，任务栏按钮上放一个圆点 overlay（Windows 的 overlay 是 16px 小图，
// 放不下可读的数字），未读数变多而窗口没有焦点时闪一下任务栏按钮提醒。
export type BadgePlan = { dock?: number; overlay?: boolean; flash?: boolean }

export function badgePlan(previous: number, next: number, p: PlatformId = current): BadgePlan {
  if (badgeStyle(p) === 'dock') return { dock: next }
  return { overlay: next > 0, flash: next > previous }
}

// 16×16 的红点（BGRA 位图，Electron 不提供画图，像素自己算），带一圈透明边与抗锯齿。
export function dotBitmap(size = 16): Buffer {
  const buf = Buffer.alloc(size * size * 4)
  const c = (size - 1) / 2
  const r = size / 2 - 1.5
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const d = Math.hypot(x - c, y - c)
      const a = Math.max(0, Math.min(1, r - d + 0.5))
      const i = (y * size + x) * 4
      // 预乘 alpha 的 BGRA：#E5484D
      buf[i] = Math.round(0x4d * a)
      buf[i + 1] = Math.round(0x48 * a)
      buf[i + 2] = Math.round(0xe5 * a)
      buf[i + 3] = Math.round(255 * a)
    }
  }
  return buf
}

let dot: NativeImage | undefined
const overlayImage = () => (dot ??= nativeImage.createFromBitmap(dotBitmap(), { width: 16, height: 16 }))

export class Badge {
  private count = 0

  constructor(
    private readonly window: () => BrowserWindow | undefined,
    private readonly platform: PlatformId = current,
    // 显示用的文字：overlay 的无障碍说明。
    private readonly describe: (count: number) => string = (n) => `${n}`,
  ) {}

  set(count: number): void {
    const plan = badgePlan(this.count, count, this.platform)
    this.count = count
    if (plan.dock !== undefined) app.setBadgeCount(plan.dock)
    const win = this.window()
    if (!win) return
    if (plan.overlay !== undefined) win.setOverlayIcon(plan.overlay ? overlayImage() : null, plan.overlay ? this.describe(count) : '')
    if (plan.flash && !win.isFocused()) win.flashFrame(true)
  }
}
