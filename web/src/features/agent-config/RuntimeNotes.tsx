import { t } from '@/i18n'
import type { RuntimeDefinition } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { Section } from './Section'

// When a runtime other than the bundled Pi is chosen, state plainly how it differs from a Pi Agent, one short point per capability it
// lacks or adds: it always runs with your real home directory and logins (and, like Pi, runs commands without asking); plugins' Pi
// extensions don't load; it has no /pi: commands. Nothing is shown for a runtime without differences, or before the definitions are read.
// brief: on creation, list only the points worth knowing the moment it is chosen (the home directory and logins, and running commands
// without confirmation); plugins and /pi: commands are left to the config page.
export function RuntimeDifferences({ runtime, className, brief = false }: { runtime: RuntimeDefinition | undefined; className?: string; brief?: boolean }) {
  if (!runtime) return null
  const copy = t.runtime.differences
  const { realHome, pluginExtensions, nativeCommands } = runtime.capabilities
  const items = [
    ...(realHome ? [copy.home, copy.commands] : []),
    ...(!brief && !pluginExtensions ? [copy.plugins] : []),
    ...(!brief && !nativeCommands ? [copy.piCommands] : []),
  ]
  if (items.length === 0) return null
  const title = copy.title(runtime.name)
  return (
    <section aria-label={title} className={cn('grid min-w-0 gap-1 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13.5px] [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue', className)}>
      <h3 className="font-semibold [[data-skin=plate]_&]:font-black">{title}</h3>
      <ul className="grid list-disc gap-0.5 pl-5">
        {items.map((item) => <li key={item}>{item}</li>)}
      </ul>
    </section>
  )
}

// The model account section for a platform Agent whose runtime uses its own login on this computer (Claude Code): there is no account
// to log in to or enter.
export function LocalAccountSection({ runtime }: { runtime: RuntimeDefinition }) {
  return (
    <Section id="model-credential" title={t.config.modelCredential.title}>
      <p className="text-[13.5px]">{t.runtime.account(runtime.name)}</p>
    </Section>
  )
}
