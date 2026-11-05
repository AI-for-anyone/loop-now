import { app, Menu } from 'electron'
import { current, type PlatformId } from './platform'
import { strings } from './strings'

// 应用菜单：macOS 上和 Electron 的默认菜单一样，只把"退出"换成自己的菜单项（Cmd+Q），由它弹退出确认框。
// 默认的 role: 'quit' 直接走 app.quit()，和信号、关机注销、Dock 菜单这些不该弹框的途径分不开。
// Windows 没有这样的应用菜单：不放菜单栏（系统标题栏下面不多一条 File／Edit），退出只从托盘走；关窗口（含 Alt+F4）只是收进托盘，
// 编辑快捷键由 Chromium 自己处理，不需要菜单里的 role。
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
