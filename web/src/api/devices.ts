import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, ApiError, call, deviceAuth, isUnreachableResponse, type Schemas } from './client'

export const deviceKeys = {
  remoteAccess: ['remote-access'] as const,
  devices: ['devices'] as const,
  relayAccount: ['relay-account'] as const,
  tunnel: ['tunnel'] as const,
  pairingStatus: (pairingId: string) => ['device-pairing', pairingId] as const,
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
    onSuccess: (state) => {
      client.setQueryData(deviceKeys.remoteAccess, state)
      void client.invalidateQueries({ queryKey: deviceKeys.tunnel })
    },
  })
}

export function useRelayAccount(enabled = true) {
  return useQuery({
    queryKey: deviceKeys.relayAccount,
    queryFn: () => call(api.GET('/relay-account')),
    refetchOnWindowFocus: true,
    refetchIntervalInBackground: true,
    refetchInterval: (query) => (query.state.data?.signIn ? 1500 : false),
    enabled,
  })
}

export function useStartRelaySignIn() {
  const client = useQueryClient()
  return useMutation<Schemas['RelaySignIn'], ApiError, void>({
    mutationFn: () => call(api.POST('/relay-account/sign-in')),
    onSuccess: (signIn) => {
      client.setQueryData<Schemas['RelayAccount']>(deviceKeys.relayAccount, (current) =>
        current ? { ...current, signIn, error: null } : current,
      )
      void client.invalidateQueries({ queryKey: deviceKeys.relayAccount })
    },
  })
}

export function useCancelRelaySignIn() {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: async () => {
      await call(api.DELETE('/relay-account/sign-in'))
    },
    onSuccess: () => {
      client.setQueryData<Schemas['RelayAccount']>(deviceKeys.relayAccount, (current) =>
        current ? { ...current, signIn: null } : current,
      )
      void client.invalidateQueries({ queryKey: deviceKeys.relayAccount })
    },
  })
}

export function useSignOutRelayAccount() {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: async () => {
      await call(api.DELETE('/relay-account'))
    },
    onSuccess: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: deviceKeys.relayAccount }),
        client.invalidateQueries({ queryKey: deviceKeys.tunnel }),
        client.invalidateQueries({ queryKey: deviceKeys.remoteAccess }),
      ])
    },
  })
}

export function tunnelShouldPoll(state: Schemas['Tunnel'] | undefined): boolean {
  if (!state) return false
  if (state.state === 'starting' || state.state === 'downloading' || state.state === 'connecting') return true
  return state.state === 'failed' && state.retryAt !== null
}

export function useTunnel(enabled = true) {
  return useQuery({
    queryKey: deviceKeys.tunnel,
    queryFn: () => call(api.GET('/tunnel')),
    refetchOnWindowFocus: true,
    refetchIntervalInBackground: true,
    refetchInterval: (query) => {
      if (tunnelShouldPoll(query.state.data)) return 1500
      return query.state.data?.state === 'online' ? 10_000 : false
    },
    enabled,
  })
}

export function useUpdateTunnel() {
  const client = useQueryClient()
  return useMutation<Schemas['Tunnel'], ApiError, Schemas['TunnelSettings']>({
    mutationFn: (body) => call(api.PUT('/tunnel', { body })),
    onSuccess: (state) => {
      client.setQueryData(deviceKeys.tunnel, state)
      void client.invalidateQueries({ queryKey: deviceKeys.remoteAccess })
      if (state.errorCode === 'signed_out') {
        void client.invalidateQueries({ queryKey: deviceKeys.relayAccount })
      }
    },
    onError: (error) => {
      if (error.code === 'relay_signed_out') {
        void client.invalidateQueries({ queryKey: deviceKeys.relayAccount })
        void client.invalidateQueries({ queryKey: deviceKeys.tunnel })
      }
    },
  })
}

export function useReleaseTunnelAddress() {
  const client = useQueryClient()
  return useMutation<void, ApiError, void>({
    mutationFn: async () => {
      await call(api.DELETE('/tunnel/address'))
    },
    onSuccess: async () => {
      await Promise.all([
        client.invalidateQueries({ queryKey: deviceKeys.tunnel }),
        client.invalidateQueries({ queryKey: deviceKeys.remoteAccess }),
      ])
    },
    onError: (error) => {
      if (error.code === 'relay_signed_out') {
        void client.invalidateQueries({ queryKey: deviceKeys.relayAccount })
        void client.invalidateQueries({ queryKey: deviceKeys.tunnel })
      }
    },
  })
}

// Pairing codes are one-time secrets and must never enter the query cache or browser storage; callers hold them in local component state only.
export function useCreateDevicePairing() {
  return useMutation<Schemas['DevicePairing'], ApiError, void>({
    mutationFn: () => call(api.POST('/device-pairings')),
  })
}

// Reads whether a specific pairing code is still open, used (and by which device), or expired.
// The response carries only the code's id and state, never the secret code or pairing link.
export function useDevicePairingStatus(pairingId: string | null) {
  return useQuery({
    queryKey: pairingId ? deviceKeys.pairingStatus(pairingId) : (['device-pairing', null] as const),
    queryFn: () => call(api.GET('/device-pairings/{pairingId}', { params: { path: { pairingId: pairingId! } } })),
    enabled: pairingId !== null,
    retry: false,
    gcTime: 0,
    refetchOnWindowFocus: true,
    refetchInterval: (query) => {
      if (query.state.error) return false
      const state = query.state.data?.state
      return state && state !== 'open' ? false : 1500
    },
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
    .then((result) => (!isUnreachableResponse(result.response) && result.response.status === 401 ? false : !deviceAuth.isUnauthenticated()))
    .catch(() => !deviceAuth.isUnauthenticated())
    .finally(() => {
      activeProbe = null
    })
  return activeProbe
}
