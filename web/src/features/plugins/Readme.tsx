import type { Schemas } from '@/api/client'
import { usePreviewReadme } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Markdown } from '@/features/chat/Markdown'
import { t } from '@/i18n'

const text = t.plugins.readme

export const TruncatedNote = () => <p className="text-[12.5px] text-ink-700">{text.truncated}</p>

export function ReadmeMarkdown({ readme }: { readme: Schemas['PluginReadme'] }) {
  return (
    <div className="min-w-0 text-[14.5px]">
      <Markdown text={readme.markdown ?? ''} readme />
    </div>
  )
}

// 安装或更新前的确认框里：清单下方的 README，单独滚动，免得把安装按钮推得很远。没有 README 时不显示。
export function PreviewReadme({ previewId }: { previewId: string }) {
  const readme = usePreviewReadme(previewId)
  if (readme.isPending || readme.data?.file === null) return null
  return (
    <section aria-label={text.title} className="grid min-w-0 gap-1.5">
      <h3 className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-[13px] font-black text-ink-700">
        {text.title}
        {readme.data && <span className="font-mono text-[12px] font-bold text-ink-500">{readme.data.file}</span>}
      </h3>
      <ErrorAlert error={readme.error} />
      {readme.data && (
        // 滚动区是定位容器，里面绝对定位的读屏文字不会撑高对话框；可聚焦，键盘也能滚动。
        <div tabIndex={0} aria-label={readme.data.file ?? text.title} className="relative max-h-[40vh] min-w-0 overflow-y-auto rounded-input bg-ink/[0.04] px-4 py-3 outline-none focus-visible:shadow-focus">
          <ReadmeMarkdown readme={readme.data} />
        </div>
      )}
      {readme.data?.truncated && <TruncatedNote />}
    </section>
  )
}
