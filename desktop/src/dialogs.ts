import { dialog, ipcMain, type BrowserWindow, type IpcMainInvokeEvent } from 'electron'
import { pickPathChannel, pickPathHandler } from './pick-path'
import type { PlatformId } from './platform'

type Options = {
  // Whether the call comes from the console page that the server serves in the window.
  trusted: (event: IpcMainInvokeEvent) => boolean
  // The console window: the dialog is attached to it (a sheet on macOS, modal on Windows).
  window: () => BrowserWindow | undefined
  platform: PlatformId
}

// Lets the console page ask for a folder or file through the system's own dialog (see pick-path.ts).
export function handlePathPicker(o: Options): void {
  const show = pickPathHandler<IpcMainInvokeEvent>({
    trusted: o.trusted,
    platform: o.platform,
    show: (options) => {
      const win = o.window()
      return win ? dialog.showOpenDialog(win, options) : dialog.showOpenDialog(options)
    },
  })
  ipcMain.handle(pickPathChannel, show)
}
