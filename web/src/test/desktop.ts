import { onTestFinished, vi } from 'vitest'
import type { DesktopBridge, PickPathRequest } from '@/features/desktop/desktop'

// The page as it runs in the desktop shell's window, for one test: the shell's system dialog answers every pick with `answer` (null:
// cancelled). withPicker: false stands for an older shell that has notifications but no picker.
export function inDesktopShell({ answer = null, withPicker = true }: { answer?: string | null; withPicker?: boolean } = {}) {
  const pickPath = vi.fn<(request: PickPathRequest) => Promise<string | null>>(async () => answer)
  const bridge: DesktopBridge = { notify: vi.fn(), setBadge: vi.fn(), onNavigate: () => () => undefined, ...(withPicker ? { pickPath } : {}) }
  window.loopNowDesktop = bridge
  onTestFinished(() => {
    delete window.loopNowDesktop
  })
  return pickPath
}
