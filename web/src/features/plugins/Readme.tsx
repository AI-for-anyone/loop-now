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

// In the confirmation dialog before installing or updating: the README below the manifest, scrolling on its own so it doesn't push the
// install button far down. Not shown when there is no README.
export function PreviewReadme({ previewId }: { previewId: string }) {
  const readme = usePreviewReadme(previewId)
  if (readme.isPending || readme.data?.file === null) return null
  return (
    <section aria-label={text.title} className="grid min-w-0 gap-1.5">
      <h3 className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-[13px] font-semibold text-ink-700">
        {text.title}
        {readme.data && <span className="font-mono text-[12px] font-medium text-ink-500">{readme.data.file}</span>}
      </h3>
      <ErrorAlert error={readme.error} />
      {readme.data && (
        // The scroll area is the positioning container, so absolutely positioned screen-reader text inside it doesn't stretch the dialog;
        // it is focusable, so it can also be scrolled with the keyboard.
        <div tabIndex={0} aria-label={readme.data.file ?? text.title} className="relative max-h-[40vh] min-w-0 overflow-y-auto rounded-input border border-hairline bg-surface-subtle px-4 py-3 outline-none focus-visible:shadow-focus">
          <ReadmeMarkdown readme={readme.data} />
        </div>
      )}
      {readme.data?.truncated && <TruncatedNote />}
    </section>
  )
}
