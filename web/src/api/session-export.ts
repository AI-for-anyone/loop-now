import { config } from '@/config'
import { ApiError, requestIdOf, toApiError } from './client'

export type SessionExportFormat = 'html' | 'jsonl'

function filenameFromDisposition(header: string | null, fallback: string): string {
  if (!header) return fallback
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(header)?.[1]
  const quoted = /filename="([^"]+)"/i.exec(header)?.[1]
  const plain = /(?:^|;)\s*filename=([^;]+)/i.exec(header)?.[1]?.trim()
  let filename = quoted ?? plain ?? ''
  if (encoded) {
    try { filename = decodeURIComponent(encoded) } catch { return fallback }
  }
  filename = filename.trim()
  const unsafe = /[\\/]/.test(filename) || [...filename].some((char) => {
    const code = char.charCodeAt(0)
    return code <= 0x1f || code === 0x7f
  })
  return filename && !unsafe ? filename : fallback
}

// 下载当前会话。只有文件已取得并交给浏览器后才算成功，调用方才显示命令结果。
export async function downloadAgentSession(agentId: string, format: SessionExportFormat): Promise<void> {
  let response: Response
  try {
    response = await fetch(`${config.apiBase}/agents/${encodeURIComponent(agentId)}/session/export?format=${encodeURIComponent(format)}`)
  } catch {
    throw new ApiError({ status: 0, code: 'unreachable' })
  }
  if (!response.ok) {
    const text = await response.text()
    let body: unknown = text
    try { body = JSON.parse(text) } catch { /* 非 JSON 错误保留原文。 */ }
    throw toApiError(response.status, body, requestIdOf(response))
  }
  const blob = await response.blob()
  const fallback = `agent-session.${format}`
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filenameFromDisposition(response.headers.get('Content-Disposition'), fallback)
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  try { anchor.click() } finally {
    anchor.remove()
    URL.revokeObjectURL(url)
  }
}
