// @loop-now/plugin-sdk: types and runtime library for the platform side of Loop Now plugins (plugins/SPEC.md §2.2, §3, §7).
// When running in the host, the host provides this package's instance; plugins list it in peerDependencies, and install it
// in devDependencies during development to get the types.

export * from './events.ts'
export type * from './types.ts'
export { createDelivery, DEFAULT_BACKOFF_MS, RECORD_PREFIX, REQUEST_PREFIX } from './delivery.ts'
export type { Delivery, DeliveryOptions, DeliveryRecord, DeliveryRequest, DeliverySigning, DeliveryStatus } from './delivery.ts'
export { DELIVERY_HEADER, SIGNATURE_HEADER, signBody, TIMESTAMP_HEADER, verifySignature } from './signature.ts'
export type { VerifyInput } from './signature.ts'
export { ulid, ULID_PATTERN } from './ulid.ts'

import type { PluginRegister } from './types.ts'

/** Only for type inference: `export default definePlugin((api) => { ... })`. */
export function definePlugin(register: PluginRegister): PluginRegister {
  return register
}
