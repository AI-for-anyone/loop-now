import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { Outlet, useLocation } from 'react-router'
import { Dialog as DialogPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { useProjects } from '@/api/queries'
import { NavIcon } from '@/components/NavIcon'
import { Toasts } from '@/components/Toasts'
import { useInboxLive } from '@/features/inbox/useInboxLive'
import { inDesktopWindow, useDesktopNavigation } from '@/features/desktop/desktop'
import { t } from '@/i18n'
import { GLOBAL, lastScope, urlScope, type ResolvedShellScope } from '@/lib/scope'
import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MIN,
  clampSidebarWidth,
  maxSidebarWidth,
  sidebarWidth,
} from '@/lib/sidebar-width'
import { cn } from '@/lib/utils'
import { ConnectionBanner } from './ConnectionBanner'
import { Sidebar } from './Sidebar'
import { SidebarResizeHandle } from './SidebarResizeHandle'

function subscribeResize(onChange: () => void) {
  window.addEventListener('resize', onChange)
  return () => window.removeEventListener('resize', onChange)
}

const getViewportWidth = () => window.innerWidth

const text = t.shell

// The scope the sidebar keeps. For a project, the resolved ID is recorded: if the project is renamed while you are on it, the same project
// is still found by ID.
type Kept = { kind: 'global' } | { kind: 'project'; name: string; id: string | null }

const sameKept = (a: Kept, b: Kept | null) =>
  a.kind === b?.kind && (a.kind === 'global' || (b.kind === 'project' && a.name === b.name && a.id === b.id))

function resolve(kept: Kept, list: Schemas['Project'][] | undefined): ResolvedShellScope {
  if (kept.kind === 'global') return GLOBAL
  const project = (kept.id ? list?.find((p) => p.id === kept.id) : undefined) ?? list?.find((p) => p.name === kept.name)
  return { kind: 'project', name: project?.name ?? kept.name, project }
}

// When a global page is opened directly and no scope has been visited yet this time: use the last-used project, or global when there is
// no record (or the record is the platform).
function remembered(list: Schemas['Project'][] | undefined): Kept {
  const project = list?.find((p) => p.id === lastScope.get())
  return project ? { kind: 'project', name: project.name, id: project.id } : GLOBAL
}

// Matches the width at which the sidebar switches between the wide and narrow layouts (--breakpoint-nav in index.css).
const wide = '(min-width: 56.3125rem)'

// The console's frame: the sidebar plus the main area. The sidebar's scope comes from the URL (project pages and the overview); global pages
// carry no scope and keep the previous one, so opening the plugin market, global settings and so on from inside a project does not lose
// the current project. At widths ≤ 900 the sidebar folds into a drawer and the top becomes a narrow blue bar.
// At widths ≥ 1024 the frame is fixed to the viewport height and each column scrolls within its own area; when narrower, the whole page
// scrolls together and the sidebar sticks to the viewport.
// In a desktop shell window, the blue bar at the top of the narrow layout also drags the window, and the menu button moves aside to the
// right of the system's traffic-light buttons.
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

  // The drawer stays open only at the URL where it was opened: clicking any entry changes the URL and closes it automatically.
  const [drawerAt, setDrawerAt] = useState<string | null>(null)
  const drawerOpen = drawerAt === location.key
  const searchOnOpen = useRef(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const drawerSearchRef = useRef<HTMLInputElement>(null)

  // ⌘K (or Ctrl+K) focuses the sidebar search; on narrow screens it opens the drawer first.
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

  // Close the drawer when the window becomes wide enough for the permanent sidebar, so no invisible overlay is left behind.
  useEffect(() => {
    const query = window.matchMedia(wide)
    const onChange = () => {
      if (query.matches) setDrawerAt(null)
    }
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  // Open one inbox event stream on entering the console; messages sent to you, the "needs you" summary and every inbox view rely on it for
  // live updates.
  useInboxLive()
  useDesktopNavigation()

  const viewportWidth = useSyncExternalStore(subscribeResize, getViewportWidth, () => 1440)
  const [preferredWidth, setPreferredWidth] = useState<number>(() => sidebarWidth.read())
  const [dragWidth, setDragWidth] = useState<number | null>(null)
  const maxWidth = maxSidebarWidth(viewportWidth)
  const currentSidebarWidth = clampSidebarWidth(dragWidth ?? preferredWidth, viewportWidth)

  return (
    <div className="riso-paper flex min-h-screen text-ink lg:h-screen">
      <div style={{ width: currentSidebarWidth }} className="sticky top-0 z-20 h-screen shrink-0 self-start max-nav:hidden">
        <Sidebar scope={scope} projects={list} projectsError={projects.error} searchRef={searchRef} />
        <SidebarResizeHandle
          width={currentSidebarWidth}
          min={SIDEBAR_WIDTH_MIN}
          max={maxWidth}
          onPreview={setDragWidth}
          onCommit={(next) => {
            setPreferredWidth(next)
            sidebarWidth.write(next)
          }}
          onReset={() => {
            setDragWidth(null)
            setPreferredWidth(SIDEBAR_WIDTH_DEFAULT)
            sidebarWidth.clear()
          }}
        />
      </div>
      <DialogPrimitive.Root open={drawerOpen} onOpenChange={(open) => setDrawerAt(open ? location.key : null)}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-ink/30 nav:hidden" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 w-[min(248px,86vw)] shadow-[6px_0_0_var(--riso-pink)] outline-none nav:hidden"
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
      <div className="riso-grain flex min-w-0 flex-1 flex-col lg:min-h-0 nav:border-l-[1.5px] nav:border-ink">
        <header
          className={cn(
            'riso-halftone sticky top-0 z-30 flex h-12 shrink-0 items-center gap-3 bg-riso-blue px-4 text-white nav:hidden',
            inDesktopWindow() && 'window-drag pl-[84px]',
          )}
        >
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
