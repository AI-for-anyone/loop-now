// 控制台页面里的 window.loopNowDesktop（页面一侧的类型与用法见 web/src/features/desktop/desktop.ts）。
// 只把调用转给主进程；主进程核对调用来自服务端提供的控制台页面，并检查内容。
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

contextBridge.exposeInMainWorld('loopNowDesktop', {
  // 壳所在的平台：页面据此决定要不要留顶部的拖动区（只有 macOS 藏了系统标题栏）。
  platform: process.platform,
  notify: (notification: unknown) => ipcRenderer.send('loop-now:notify', notification),
  setBadge: (count: unknown) => ipcRenderer.send('loop-now:badge', count),
  onNavigate: (listener: (path: string) => void) => {
    const handler = (_event: IpcRendererEvent, path: string) => listener(path)
    ipcRenderer.on('loop-now:navigate', handler)
    return () => void ipcRenderer.removeListener('loop-now:navigate', handler)
  },
})
