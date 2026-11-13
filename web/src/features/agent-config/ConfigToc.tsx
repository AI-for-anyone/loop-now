import { useEffect, useState } from 'react'
import { useLocation } from 'react-router'
import { RulerMark, RulerTicks, Stub } from '@/components/Sheet'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

const titles: Record<SectionKey, string> = {
  basic: t.config.basic.title,
  prompt: t.prompts.sectionTitle,
  'local-dirs': t.localMode.directories.title,
  'model-credential': t.config.modelCredential.title,
  skills: t.config.skills.title,
  clis: t.config.clis.title,
  plugins: t.plugins.agentTitle,
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

// 分区目录：纸张右侧票根里的一把目录尺（改版通用规则第 5 条）。右缘一排刻度，每项是“名称 编号 短刻度”，
// 当前区块那一项用略微倾斜的黄色记号笔加粉色小箭头标出；删除 Agent 一项是红色。窄屏（左侧栏收进抽屉时）不显示。
// 点击目录项滚动到对应区块，并把焦点移到区块标题；不用平滑滚动，控制台只保留三个动效。
// 编号在 DOM 里排在名称前面（读屏读成“01 基本信息”），显示时用 order 排到名称后面。
export function ConfigToc({ className }: { className?: string }) {
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
    <Stub as="nav" aria-label={t.config.toc.title} label={t.config.toc.title} className={cn('w-[196px] overflow-x-hidden overflow-y-auto', className)}>
      <ol className="relative flex flex-col text-[13.5px] font-bold">
        <RulerTicks className="bg-[repeating-linear-gradient(to_bottom,transparent_0,transparent_10px,var(--ink)_10px,var(--ink)_12px,transparent_12px,transparent_23px)]" />
        {SECTION_KEYS.map((key, index) => {
          const current = key === active
          const danger = key === 'danger'
          return (
            <li key={key} className="relative">
              {current && <RulerMark data-ruler-mark className="top-[5px] h-9" />}
              <a
                href={`#${sectionId(key)}`}
                aria-current={current ? 'location' : undefined}
                onClick={(event) => {
                  event.preventDefault()
                  jump(key)
                }}
                className={cn(
                  'relative flex h-[46px] items-center justify-end gap-2.5 pl-[18px] no-underline outline-none focus-visible:shadow-focus',
                  current ? 'font-black text-ink' : 'text-ink-700 hover:text-ink',
                  danger && 'text-red-text hover:text-red-text',
                )}
              >
                <span aria-hidden="true" className="order-2 font-mono text-[10.5px]">
                  {String(index + 1).padStart(2, '0')}
                </span>
                <span className="order-1 min-w-0 truncate">{titles[key]}</span>
                <span aria-hidden="true" className={cn('order-3 h-0.5 w-6 shrink-0', danger ? 'bg-red-text' : 'bg-ink')} />
              </a>
            </li>
          )
        })}
      </ol>
    </Stub>
  )
}
