import { useEffect, useRef, useState } from 'react'
import { Outlet, useLocation } from 'react-router'
import { Dialog as DialogPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { useProjects } from '@/api/queries'
import { NavIcon } from '@/components/NavIcon'
import { Toasts } from '@/components/Toasts'
import { useInboxLive } from '@/features/inbox/useInboxLive'
import { t } from '@/i18n'
import { GLOBAL, lastScope, urlScope, type ResolvedShellScope } from '@/lib/scope'
import { ConnectionBanner } from './ConnectionBanner'
import { Sidebar } from './Sidebar'

const text = t.shell

// 左侧栏沿用的范围。项目记下解析到的 ID：停留期间项目改名，仍按 ID 找到同一个项目。
type Kept = { kind: 'global' } | { kind: 'project'; name: string; id: string | null }

const sameKept = (a: Kept, b: Kept | null) =>
  a.kind === b?.kind && (a.kind === 'global' || (b.kind === 'project' && a.name === b.name && a.id === b.id))

function resolve(kept: Kept, list: Schemas['Project'][] | undefined): ResolvedShellScope {
  if (kept.kind === 'global') return GLOBAL
  const project = (kept.id ? list?.find((p) => p.id === kept.id) : undefined) ?? list?.find((p) => p.name === kept.name)
  return { kind: 'project', name: project?.name ?? kept.name, project }
}

// 直接打开全局页面、本次还没有到过任何范围时：用最近使用的项目，没有记录（或记录的是平台）时为全局。
function remembered(list: Schemas['Project'][] | undefined): Kept {
  const project = list?.find((p) => p.id === lastScope.get())
  return project ? { kind: 'project', name: project.name, id: project.id } : GLOBAL
}

// 与左侧栏切换宽屏、窄屏的宽度一致（index.css 里的 --breakpoint-nav）。
const wide = '(min-width: 56.3125rem)'

// 控制台的外框：左侧栏加主区。左侧栏的范围来自地址（项目页与概览）；全局页面不带范围，沿用之前的范围，
// 所以从项目里打开插件市场、全局设置等不会丢掉当前项目。宽度 ≤ 900 时左侧栏收进抽屉，顶部换成一条窄的蓝色栏。
// 宽度 ≥ 1024 时外框固定为视口高度，各栏在自己的区域内滚动；更窄时整页一起滚动，左侧栏贴在视口里。
export function AppLayout() {
  const location = useLocation()
  const projects = useProjects()
  const list = projects.data?.projects
  const fromUrl = urlScope(location)
  const [kept, setKept] = useState<Kept | null>(null)
  let next: Kept | null = null
  if (fromUrl?.kind === 'global') next = GLOBAL
  else if (fromUrl) {
    const found = list?.find((p) => p.name === fromUrl.name)
    const previous = kept?.kind === 'project' && kept.name === fromUrl.name ? kept.id : null
    next = { kind: 'project', name: fromUrl.name, id: found?.id ?? previous }
  }
  if (next && !sameKept(next, kept)) setKept(next)
  const scope = resolve(next ?? kept ?? remembered(list), list)

  // 抽屉只在打开它时的地址上保持打开：点任何入口换了地址就自动关上。
  const [drawerAt, setDrawerAt] = useState<string | null>(null)
  const drawerOpen = drawerAt === location.key
  const searchOnOpen = useRef(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const drawerSearchRef = useRef<HTMLInputElement>(null)

  // ⌘K（或 Ctrl+K）聚焦左侧栏的搜索；窄屏时先打开抽屉。
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey || event.key.toLowerCase() !== 'k') return
      event.preventDefault()
      const input = drawerSearchRef.current ?? searchRef.current
      input?.focus()
      if (input && document.activeElement === input) return
      searchOnOpen.current = true
      setDrawerAt(location.key)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [location.key])

  // 窗口变宽到常驻左侧栏时关上抽屉，不留下看不见的遮罩。
  useEffect(() => {
    const query = window.matchMedia(wide)
    const onChange = () => {
      if (query.matches) setDrawerAt(null)
    }
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  // 进入控制台后建立一条收件箱事件流，发给你的消息、待你处理的汇总与各个收件箱视图都靠它实时更新。
  useInboxLive()
  return (
    <div className="riso-paper flex min-h-screen text-ink lg:h-screen">
      <div className="sticky top-0 h-screen w-[244px] shrink-0 self-start max-nav:hidden">
        <Sidebar scope={scope} projects={list} projectsError={projects.error} searchRef={searchRef} />
      </div>
      <DialogPrimitive.Root open={drawerOpen} onOpenChange={(open) => setDrawerAt(open ? location.key : null)}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-ink/30 nav:hidden" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 w-[min(244px,86vw)] shadow-[6px_0_0_var(--riso-pink)] outline-none nav:hidden"
            onOpenAutoFocus={(event) => {
              if (!searchOnOpen.current) return
              searchOnOpen.current = false
              event.preventDefault()
              drawerSearchRef.current?.focus()
            }}
          >
            <DialogPrimitive.Title className="sr-only">{text.nav}</DialogPrimitive.Title>
            <Sidebar scope={scope} projects={list} projectsError={projects.error} searchRef={drawerSearchRef} onClose={() => setDrawerAt(null)} />
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
      <div className="flex min-w-0 flex-1 flex-col lg:min-h-0">
        <header className="riso-halftone sticky top-0 z-30 flex h-12 shrink-0 items-center gap-3 bg-riso-blue px-4 text-white nav:hidden">
          <button
            type="button"
            aria-label={text.menu}
            aria-expanded={drawerOpen}
            onClick={() => setDrawerAt(location.key)}
            className="inline-flex size-9 items-center justify-center rounded-ticket border-[1.5px] border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none focus-visible:shadow-focus"
          >
            <NavIcon name="menu" className="[stroke-width:2.6]" />
          </button>
          <span className="font-display-latin text-[19px] leading-none font-extrabold tracking-[-0.5px] [text-shadow:2px_2px_0_var(--riso-pink)]">{t.app.brand}</span>
        </header>
        <ConnectionBanner />
        <main className="min-h-0 flex-1">
          <Outlet />
        </main>
      </div>
      <Toasts />
    </div>
  )
}
