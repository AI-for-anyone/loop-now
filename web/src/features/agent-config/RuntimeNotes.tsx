import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { Section } from './Section'

// 选了 Claude Code 时，如实写出它和 Pi 的 Agent 不同的地方；文字简短，逐条列出。
// brief：新建时只列选的那一刻就该知道的两条（本机家目录与登录、不经确认执行命令），插件与 /pi: 命令留在配置页。
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

// 平台 Agent 的模型账号一节：Claude Code 用本机自己的登录，没有账号可登录或录入。
export function ClaudeCodeAccountSection() {
  return (
    <Section id="model-credential" title={t.config.modelCredential.title}>
      <p className="text-[13.5px]">{t.runtime.account}</p>
    </Section>
  )
}
