// @loop-now/plugin-sdk：Loop Now 插件平台半边的类型与运行库（plugins/SPEC.md §2.2、§3、§7）。
// 在宿主里运行时，宿主提供这个包的实例；插件把它放在 peerDependencies，开发时装在 devDependencies 里取类型。

export * from './events.ts'
export type * from './types.ts'
export { createDelivery, DEFAULT_BACKOFF_MS, RECORD_PREFIX, REQUEST_PREFIX } from './delivery.ts'
export type { Delivery, DeliveryOptions, DeliveryRecord, DeliveryRequest, DeliverySigning, DeliveryStatus } from './delivery.ts'
export { DELIVERY_HEADER, SIGNATURE_HEADER, signBody, TIMESTAMP_HEADER, verifySignature } from './signature.ts'
export type { VerifyInput } from './signature.ts'
export { ulid, ULID_PATTERN } from './ulid.ts'

import type { PluginRegister } from './types.ts'

/** 只为类型推断：`export default definePlugin((api) => { ... })`。 */
export function definePlugin(register: PluginRegister): PluginRegister {
  return register
}
