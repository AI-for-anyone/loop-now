import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { Section } from './Section'

// When Claude Code is chosen, state plainly how it differs from a Pi Agent; keep the text short, one point per item.
// brief: on creation, list only the two points worth knowing the moment it is chosen (the local home directory and login, and running
// commands without confirmation); plugins and /pi: commands are left to the config page.
export function ClaudeCodeDifferences({ className, brief = false }: { className?: string; brief?: boolean }) {
  const copy = t.runtime.differences
  return (
    <section aria-label={copy.title} className={cn('grid min-w-0 gap-1 rounded-input bg-tint-blue px-3 py-2 text-[13.5px] [overflow-wrap:anywhere]', className)}>
      <h3 className="font-black">{copy.title}</h3>
      <ul className="grid list-disc gap-0.5 pl-5">
        <li>{copy.home}</li>
        <li>{copy.commands}</li>
        {!brief && <li>{copy.plugins}</li>}
        {!brief && <li>{copy.piCommands}</li>}
      </ul>
    </section>
  )
}

// The model account section for a platform Agent: Claude Code uses this machine's own login, so there is no account to log in to or enter.
export function ClaudeCodeAccountSection() {
  return (
    <Section id="model-credential" title={t.config.modelCredential.title}>
      <p className="text-[13.5px]">{t.runtime.account}</p>
    </Section>
  )
}
