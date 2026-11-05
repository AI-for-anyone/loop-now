import { app, ipcMain, Notification, type IpcMainEvent } from 'electron'
import { parseBadge, parseNotification } from './bridge'

type Options = {
  // 调用是否来自窗口里服务端提供的控制台页面。
  trusted: (event: IpcMainEvent) => boolean
  // 点了通知：显示窗口，让控制台跳到 path。
  open: (path: string) => void
}

// 最多留着这么多条通知的引用：macOS 上通知对象被回收后，点它不再回调。
const keep = 100

// 接收页面发来的系统通知与 Dock 未读数。发什么、什么时候发都由页面决定，壳只负责显示。
export function handleNotifications(o: Options): void {
  const shown = new Map<string, Notification>()
  ipcMain.on('loop-now:notify', (event, value: unknown) => {
    const n = parseNotification(value)
    if (!n || !o.trusted(event) || !Notification.isSupported()) return
    const notification = new Notification({ title: n.title, body: n.body })
    const forget = () => shown.delete(n.id)
    notification.on('click', () => {
      forget()
      o.open(n.path)
    })
    notification.on('close', forget)
    shown.set(n.id, notification)
    if (shown.size > keep) shown.delete(shown.keys().next().value as string)
    notification.show()
  })
  ipcMain.on('loop-now:badge', (event, value: unknown) => {
    const count = parseBadge(value)
    if (count !== undefined && o.trusted(event)) app.setBadgeCount(count)
  })
}
