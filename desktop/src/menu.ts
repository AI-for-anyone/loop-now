import { app, Menu } from 'electron'
import { strings } from './strings'

// 应用菜单：和 Electron 的默认菜单一样，只把"退出"换成自己的菜单项（Cmd+Q），由它弹退出确认框。
// 默认的 role: 'quit' 直接走 app.quit()，和信号、关机注销、Dock 菜单这些不该弹框的途径分不开。
export function setAppMenu(quit: () => void): void {
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
