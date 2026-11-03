import { connect, createServer } from 'node:net'

// 第一次启动交给系统选端口，不去碰 8780（单独部署的服务端在用）；之后沿用记住的端口
// （控制台的语言、范围等存在按源区分的 localStorage 里，端口变了就丢）。

// 有没有进程在这个端口上接受连接。macOS 上别的进程监听通配地址时，本机地址仍能再绑定成功，
// 只试监听会把它遮住，所以先试连接。
function accepts(port: number, host: string): Promise<boolean> {
  return new Promise((resolve) => {
    const socket = connect({ port, host })
    const done = (result: boolean) => {
      socket.destroy()
      resolve(result)
    }
    socket.setTimeout(500, () => done(false))
    socket.once('connect', () => done(true))
    socket.once('error', () => done(false))
  })
}

// 能不能在 127.0.0.1 上独占监听这个端口。只是试探，紧接着就关掉，真正监听的是服务端。
function canListen(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const server = createServer()
    server.once('error', () => resolve(false))
    server.listen({ port, host: '127.0.0.1', exclusive: true }, () => server.close(() => resolve(true)))
  })
}

export async function portFree(port: number): Promise<boolean> {
  if ((await accepts(port, '127.0.0.1')) || (await accepts(port, '::1'))) return false
  return canListen(port)
}

// 想用的端口空闲就用它，否则返回 0 交给系统选，实际端口从就绪文件里读。
export async function choosePort(wanted: number): Promise<number> {
  return wanted !== 0 && (await portFree(wanted)) ? wanted : 0
}

export function portOf(url: string): number | undefined {
  const port = Number(new URL(url).port)
  return Number.isInteger(port) && port > 0 ? port : undefined
}
