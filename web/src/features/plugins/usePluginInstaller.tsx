import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { previewPlugin } from '@/api/plugins'
import { ConfirmInstallDialog } from './InstallFlow'

type Preview = Schemas['PluginPreview']

// 安装与更新共用的第一步：取来源的预览，成功后打开确认框。begin 的 key 用来标出是哪个按钮在读取。
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
