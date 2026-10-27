import { createHmac, timingSafeEqual } from 'node:crypto'

// 外发请求的签名：HMAC-SHA256(secret, "<timestamp>.<body>")，十六进制，头的值为 "sha256=<hex>"。
// 时间戳是发出这一次尝试时的 Unix 秒，每次重试重新签名，接收方可以拒绝过旧的请求防重放。

export const DELIVERY_HEADER = 'x-loop-now-delivery'
export const TIMESTAMP_HEADER = 'x-loop-now-timestamp'
export const SIGNATURE_HEADER = 'x-loop-now-signature'

export function signBody(secret: string, timestamp: number | string, body: string | Uint8Array): string {
  const mac = createHmac('sha256', secret)
  mac.update(`${timestamp}.`)
  mac.update(body)
  return `sha256=${mac.digest('hex')}`
}

export interface VerifyInput {
  secret: string
  /** 收到的原始正文（不要先解析再序列化）。 */
  body: string | Uint8Array
  /** 签名头与时间头的值。 */
  signature: string | null | undefined
  timestamp: string | null | undefined
  /** 允许的时钟偏差，默认 300 秒；0 表示不检查时间。 */
  toleranceSeconds?: number
  now?: number
}

/** 接收方校验签名；时间超出容差或签名不符时返回 false。 */
export function verifySignature(input: VerifyInput): boolean {
  const { secret, body, signature, timestamp } = input
  if (!signature || !timestamp || !/^\d+$/.test(timestamp)) return false
  const tolerance = input.toleranceSeconds ?? 300
  const now = Math.floor((input.now ?? Date.now()) / 1000)
  if (tolerance > 0 && Math.abs(now - Number(timestamp)) > tolerance) return false
  const expected = Buffer.from(signBody(secret, timestamp, body))
  const got = Buffer.from(signature)
  return expected.length === got.length && timingSafeEqual(expected, got)
}
