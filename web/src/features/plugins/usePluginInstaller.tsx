import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { previewPlugin } from '@/api/plugins'
import { ConfirmInstallDialog } from './InstallFlow'

type Preview = Schemas['PluginPreview']

// The first step shared by installing and updating: fetch a preview of the source, and on success open the confirmation dialog. The key
// passed to begin marks which button is loading.
export function usePluginInstaller(onInstalled?: (plugin: Schemas['Plugin']) => void) {
  const [preview, setPreview] = useState<Preview | null>(null)
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useState<{ key: string; error: unknown } | null>(null)
  const begin = async (key: string, body: Schemas['PluginPreviewRequest']) => {
    setPending(key)
    setError(null)
    try {
      setPreview(await previewPlugin(body))
      return true
    } catch (cause) {
      setError({ key, error: cause })
      return false
    } finally {
      setPending(null)
    }
  }
  const dialog = preview && <ConfirmInstallDialog key={preview.previewId} preview={preview} onClose={() => setPreview(null)} onInstalled={onInstalled} />
  return { begin, pending, errorFor: (key: string) => (error?.key === key ? error.error : null), dialog }
}
