import { app, nativeImage, type BrowserWindow, type NativeImage } from 'electron'
import { badgeStyle, current, type PlatformId } from './platform'

// How the unread count is shown: on macOS as a Dock badge; Windows has no Dock, so a dot overlay goes on the taskbar button (a Windows
// overlay is a 16px icon, too small for a readable number), and when the count grows while the window is unfocused, the taskbar button
// flashes once as a reminder.
export type BadgePlan = { dock?: number; overlay?: boolean; flash?: boolean }

export function badgePlan(previous: number, next: number, p: PlatformId = current): BadgePlan {
  if (badgeStyle(p) === 'dock') return { dock: next }
  return { overlay: next > 0, flash: next > previous }
}

// A 16×16 red dot (a BGRA bitmap; Electron offers no drawing, so the pixels are computed by hand), with a transparent margin and anti-aliasing.
export function dotBitmap(size = 16): Buffer {
  const buf = Buffer.alloc(size * size * 4)
  const c = (size - 1) / 2
  const r = size / 2 - 1.5
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const d = Math.hypot(x - c, y - c)
      const a = Math.max(0, Math.min(1, r - d + 0.5))
      const i = (y * size + x) * 4
      // BGRA with premultiplied alpha: #E5484D
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
    // Text to show: the overlay's accessibility description.
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
