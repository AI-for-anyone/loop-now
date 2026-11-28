import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, deviceAuth, type Schemas } from './client'

export const deviceKeys = {
  remoteAccess: ['remote-access'] as const,
  devices: ['devices'] as const,
}

export function useRemoteAccess(enabled = true) {
  return useQuery({
    queryKey: deviceKeys.remoteAccess,
    queryFn: () => call(api.GET('/remote-access')),
    enabled,
  })
}

export function useUpdateRemoteAccess() {
  const client = useQueryClient()
  return useMutation<Schemas['RemoteAccess'], ApiError, Schemas['RemoteAccessSettings']>({
    mutationFn: (body) => call(api.PUT('/remote-access', { body })),
    onSuccess: (state) => client.setQueryData(deviceKeys.remoteAccess, state),
  })
}

// Pairing codes are one-time secrets and must never enter the query cache or browser storage; callers hold them in local component state only.
export function useCreateDevicePairing() {
  return useMutation<Schemas['DevicePairing'], ApiError, void>({
    mutationFn: () => call(api.POST('/device-pairings')),
  })
}

export function useDevices(enabled = true, options?: { refetchInterval?: number | false }) {
  return useQuery({
    queryKey: deviceKeys.devices,
    queryFn: () => call(api.GET('/devices')),
    refetchOnWindowFocus: true,
    refetchInterval: options?.refetchInterval ?? false,
    enabled,
  })
}

export function useRenameDevice() {
  const client = useQueryClient()
  return useMutation<Schemas['Device'], ApiError, { deviceId: string; name: string }>({
    mutationFn: ({ deviceId, name }) =>
      call(api.PATCH('/devices/{deviceId}', { params: { path: { deviceId } }, body: { name } })),
    onSuccess: (updated) => {
      client.setQueryData<Schemas['DeviceList']>(deviceKeys.devices, (current) =>
        current ? { devices: current.devices.map((item) => (item.id === updated.id ? updated : item)) } : current,
      )
    },
  })
}

export function useRemoveDevice() {
  const client = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: async (deviceId) => {
      await call(api.DELETE('/devices/{deviceId}', { params: { path: { deviceId } } }))
    },
    onSuccess: async (_result, deviceId) => {
      client.setQueryData<Schemas['DeviceList']>(deviceKeys.devices, (current) =>
        current ? { devices: current.devices.filter((item) => item.id !== deviceId) } : current,
      )
      await client.invalidateQueries({ queryKey: deviceKeys.devices })
    },
  })
}

export function createDeviceSession(body: Schemas['DeviceSessionRequest']): Promise<Schemas['Device']> {
  return call(api.POST('/device-sessions', { body }))
}

// Auto-redemption from /pair#<code> runs in an effect, which React StrictMode invokes twice in development.
// Deduplicate calls for the same code within a short window so StrictMode never posts a one-time code twice, while a later visit
// attempting to reuse the code still hits the server and receives pairing_code_invalid.
// Network-unreachable failures are evicted right away so the user can retry once connectivity returns.
const REDEMPTION_DEDUP_MS = 2000
const redemptions = new Map<string, { promise: Promise<Schemas['Device']>; at: number }>()

export function redeemPairingCode(code: string, name?: string): Promise<Schemas['Device']> {
  const now = Date.now()
  const existing = redemptions.get(code)
  if (existing && now - existing.at < REDEMPTION_DEDUP_MS) return existing.promise
  const body: Schemas['DeviceSessionRequest'] = name ? { code, name } : { code }
  const promise = createDeviceSession(body).catch((error) => {
    if (error instanceof ApiError && error.code === 'unreachable') redemptions.delete(code)
    throw error
  })
  redemptions.set(code, { promise, at: now })
  return promise
}

// Only for tests, to reset between cases.
export function resetPairingRedemptions() {
  redemptions.clear()
}

// Extract a pairing code from either a full pairing URL (<base>/pair#<code>), a fragment (#<code>), or a bare code.
export function extractPairingCode(input: string): string {
  const trimmed = input.trim()
  if (!trimmed) return ''
  const hashIndex = trimmed.indexOf('#')
  const raw = hashIndex >= 0 ? trimmed.slice(hashIndex + 1).trim() : trimmed
  if (!raw) return ''
  try {
    return decodeURIComponent(raw).trim()
  } catch {
    return raw
  }
}

// Probe whether this browser still holds a valid device session (called when an open SSE stream drops or closes).
// Deduplicates concurrent probes; returns false when the remote door answers 401 so SSE stops reconnecting.
let activeProbe: Promise<boolean> | null = null

export function checkDeviceSession(): Promise<boolean> {
  if (deviceAuth.isUnauthenticated()) return Promise.resolve(false)
  if (activeProbe) return activeProbe
  activeProbe = api
    .GET('/context')
    .then((result) => result.response.status !== 401 && !deviceAuth.isUnauthenticated())
    .catch(() => !deviceAuth.isUnauthenticated())
    .finally(() => {
      activeProbe = null
    })
  return activeProbe
}
