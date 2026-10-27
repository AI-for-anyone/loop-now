import { pluginText } from '@/i18n/plugins'
import { useEffect, useId, useState } from 'react'
import { useLocation } from 'react-router'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

const titles: Record<SectionKey, string> = {
  basic: t.config.basic.title,
  prompt: t.prompts.sectionTitle,
  'model-credential': t.config.modelCredential.title,
  skills: t.config.skills.title,
  clis: t.config.clis.title,
  plugins: pluginText.agentTitle,
  danger: t.config.danger.title,
}

const observable = () => typeof IntersectionObserver !== 'undefined'

// 区块进入视口上部（上方 45%）时算作当前区块，多个同时可见时取靠前的一个。
// 浏览器不支持 IntersectionObserver 时不高亮，其余功能不受影响。
function useActiveSection() {
  const [active, setActive] = useState<SectionKey | null>(null)
  useEffect(() => {
    if (!observable()) return
    const visible = new Set<string>()
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) visible.add(entry.target.id)
          else visible.delete(entry.target.id)
        }
        const first = SECTION_KEYS.find((key) => visible.has(sectionId(key)))
        if (first) setActive(first)
      },
      { rootMargin: '0px 0px -55% 0px' },
    )
    for (const key of SECTION_KEYS) {
      const element = document.getElementById(sectionId(key))
      if (element) observer.observe(element)
    }
    return () => observer.disconnect()
  }, [])
  return [active, setActive] as const
}

// 分区目录：宽屏时在右侧吸顶，窄屏时移到页面上方，改为可以换行的一排链接。
// 点击目录项滚动到对应区块，并把焦点移到区块标题；不用平滑滚动，控制台只保留三个动效。
export function ConfigToc({ className }: { className?: string }) {
  const titleId = useId()
  const [active, setActive] = useActiveSection()
  const { hash } = useLocation()

  // 带着区块地址打开配置页（例如统计页的“去看它的提示词”）时，滚动到那个区块并把焦点移到它的标题。
  useEffect(() => {
    const key = SECTION_KEYS.find((item) => hash === `#${sectionId(item)}`)
    if (!key) return
    document.getElementById(sectionId(key))?.scrollIntoView({ block: 'start' })
    document.getElementById(sectionTitleId(key))?.focus({ preventScroll: true })
  }, [hash])

  const jump = (key: SectionKey) => {
    // 先按点击的目录项高亮：靠近页尾的区块滚动后可能到不了视口上部。不支持观察时不高亮，免得滚动后高亮停在旧的区块上。
    if (observable()) setActive(key)
    document.getElementById(sectionId(key))?.scrollIntoView({ block: 'start' })
    document.getElementById(sectionTitleId(key))?.focus({ preventScroll: true })
  }

  return (
    <nav aria-labelledby={titleId} className={cn('flex flex-col gap-3', className)}>
      <span id={titleId} className="font-display-cn text-[26px] leading-none">
        {t.config.toc.title}
      </span>
      <ol className="flex flex-wrap gap-1.5 lg:flex-col">
        {SECTION_KEYS.map((key, index) => {
          const current = key === active
          return (
            <li key={key}>
              <a
                href={`#${sectionId(key)}`}
                aria-current={current ? 'location' : undefined}
                onClick={(event) => {
                  event.preventDefault()
                  jump(key)
                }}
                className={cn(
                  'flex min-h-10 items-center gap-2.5 rounded-input px-3 text-[14px] text-ink no-underline focus-visible:shadow-focus',
                  current ? 'bg-riso-yellow font-black shadow-[3px_3px_0_var(--riso-pink)]' : 'font-bold hover:bg-tint-blue',
                )}
              >
                <span aria-hidden="true" className="font-display-latin text-[15px] font-extrabold text-pink-text">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <span className={cn(key === 'danger' && 'text-red-text')}>{titles[key]}</span>
              </a>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
