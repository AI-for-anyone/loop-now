import { ipcMain, Notification, type IpcMainEvent } from 'electron'
import { parseBadge, parseNotification } from './bridge'

type Options = {
  // Whether the call comes from the console page that the server serves in the window.
  trusted: (event: IpcMainEvent) => boolean
  // A notification was clicked: show the window and have the console navigate to path.
  open: (path: string) => void
  // Unread count (Dock badge or taskbar overlay, see badge.ts).
  badge: (count: number) => void
}

// Keep references to at most this many notifications: on macOS, once a notification object is garbage-collected, clicking it no longer
// calls back.
const keep = 100

// Receives system notifications and the unread count from the page. The page decides what to send and when; the shell only displays them.
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
    if (count !== undefined && o.trusted(event)) o.badge(count)
  })
}
