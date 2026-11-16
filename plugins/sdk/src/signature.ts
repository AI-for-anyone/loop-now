import { createHmac, timingSafeEqual } from 'node:crypto'

// Signature for outgoing requests: HMAC-SHA256(secret, "<timestamp>.<body>") in hex; the header value is "sha256=<hex>".
// The timestamp is the Unix seconds when this attempt is sent and every retry is signed again, so receivers can reject stale requests
// to prevent replay.

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
  /** The raw body as received (do not parse and re-serialize it). */
  body: string | Uint8Array
  /** Values of the signature header and the timestamp header. */
  signature: string | null | undefined
  timestamp: string | null | undefined
  /** Allowed clock skew, 300 seconds by default; 0 disables the time check. */
  toleranceSeconds?: number
  now?: number
}

/** For receivers to verify a signature; returns false when the time is outside the tolerance or the signature does not match. */
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
