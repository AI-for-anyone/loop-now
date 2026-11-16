import { connect, createServer } from 'node:net'

// On the first start the system picks the port, keeping away from 8780 (used by a standalone server deployment); later starts reuse the
// remembered port (the console's language, scope, etc. are stored in per-origin localStorage and are lost if the port changes).

// Whether some process accepts connections on this port. On macOS, when another process listens on the wildcard address, binding the
// loopback address still succeeds, so trying only to listen would hide it; that is why a connection is tried first.
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

// Whether this port can be listened on exclusively at 127.0.0.1. Only a probe that is closed right away; the server does the real listening.
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

// Use the wanted port if it is free; otherwise return 0 to let the system pick, and the actual port is read from the ready file.
export async function choosePort(wanted: number): Promise<number> {
  return wanted !== 0 && (await portFree(wanted)) ? wanted : 0
}

export function portOf(url: string): number | undefined {
  const port = Number(new URL(url).port)
  return Number.isInteger(port) && port > 0 ? port : undefined
}
