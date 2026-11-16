import { app, Menu } from 'electron'
import { current, type PlatformId } from './platform'
import { strings } from './strings'

// Application menu: on macOS the same as Electron's default menu, except that "退出" (Quit) is replaced with our own menu item (Cmd+Q),
// which shows the quit confirmation dialog.
// The default role: 'quit' goes straight to app.quit() and cannot be told apart from the paths that must not show a dialog: signals,
// shutdown/log off, the Dock menu.
// Windows has no such application menu: no menu bar (no extra File/Edit strip below the system title bar), and quitting only goes through
// the tray; closing the window (including Alt+F4) just tucks it into the tray, and Chromium handles the editing shortcuts itself, so no
// menu roles are needed.
export function setAppMenu(quit: () => void, p: PlatformId = current): void {
  if (p !== 'darwin') {
    Menu.setApplicationMenu(null)
    return
  }
  Menu.setApplicationMenu(
    Menu.buildFromTemplate([
      {
        label: app.name,
        submenu: [
          { role: 'about' },
          { type: 'separator' },
          { role: 'services' },
          { type: 'separator' },
          { role: 'hide' },
          { role: 'hideOthers' },
          { role: 'unhide' },
          { type: 'separator' },
          { label: strings().tray.quit, accelerator: 'Command+Q', click: quit },
        ],
      },
      { role: 'fileMenu' },
      { role: 'editMenu' },
      { role: 'viewMenu' },
      { role: 'windowMenu' },
    ]),
  )
}
