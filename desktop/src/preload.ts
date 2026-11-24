// window.loopNowDesktop in the console page (for the page-side types and usage see web/src/features/desktop/desktop.ts).
// It only forwards calls to the main process; the main process verifies that a call comes from the console page served by the server and
// checks its content.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from 'electron'

contextBridge.exposeInMainWorld('loopNowDesktop', {
  // The shell's platform: the page uses it to decide whether to leave a drag area at the top (only macOS hides the system title bar).
  platform: process.platform,
  notify: (notification: unknown) => ipcRenderer.send('loop-now:notify', notification),
  setBadge: (count: unknown) => ipcRenderer.send('loop-now:badge', count),
  // Shows the system's own dialog for a folder or file and resolves to the full path, or null when cancelled (see pick-path.ts; the
  // sandboxed preload can't import the channel name from there).
  pickPath: (request: unknown) => ipcRenderer.invoke('loop-now:pick-path', request),
  onNavigate: (listener: (path: string) => void) => {
    const handler = (_event: IpcRendererEvent, path: string) => listener(path)
    ipcRenderer.on('loop-now:navigate', handler)
    return () => void ipcRenderer.removeListener('loop-now:navigate', handler)
  },
})
