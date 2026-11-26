import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router'
import { Dialog as DialogPrimitive } from 'radix-ui'
import { Settings } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useProjects } from '@/api/queries'
import { BrandWordmark } from '@/components/BrandWordmark'
import { NavIcon } from '@/components/NavIcon'
import { Toasts } from '@/components/Toasts'
import { useInboxLive } from '@/features/inbox/useInboxLive'
import { hasInsetTitleBar, useDesktopNavigation } from '@/features/desktop/desktop'
import { t } from '@/i18n'
import { GLOBAL, lastScope, urlScope, type ResolvedShellScope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { CommandPalette } from './CommandPalette'
import { ConnectionBanner } from './ConnectionBanner'
import { Sidebar } from './Sidebar'
import { SidebarContext } from './sidebar-context'

const text = t.shell

export const SIDEBAR_COLLAPSED_KEY = 'loop-now.sidebar-collapsed'

function readSidebarCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY) === 'true'
  } catch {
    return false
  }
}

function writeSidebarCollapsed(collapsed: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_COLLAPSED_KEY, String(collapsed))
  } catch {
    // When browser storage is unavailable, collapse state still works for the current session.
  }
}

function isEditableElement(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement || el instanceof HTMLSelectElement) return true
  if (el instanceof HTMLElement && el.isContentEditable) return true
  return el.closest('[contenteditable="true"], [contenteditable=""], [role="textbox"]') !== null
}

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
// At widths ≥ 901 (nav:) the frame is fixed to the viewport height and each column scrolls within its own area; when narrower, the whole page
// scrolls together and the sidebar folds into a drawer.
// In a desktop shell window, the blue bar at the top of the narrow layout also drags the window, and the menu button moves aside to the
// right of the system's traffic-light buttons.
export function AppLayout() {
  const location = useLocation()
  const navigate = useNavigate()
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
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readSidebarCollapsed)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const desktop = hasInsetTitleBar()

  const setCollapsed = useCallback((next: boolean | ((prev: boolean) => boolean)) => {
    setSidebarCollapsed((prev) => {
      const resolved = typeof next === 'function' ? next(prev) : next
      writeSidebarCollapsed(resolved)
      return resolved
    })
  }, [])

  // Global keyboard shortcuts: ⌘K / Ctrl+K toggles the command palette; ⌘B / Ctrl+B toggles the desktop sidebar
  // (skipped while editing text).
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey) return
      const key = event.key.toLowerCase()
      if (key === 'k') {
        event.preventDefault()
        setDrawerAt(null)
        setPaletteOpen((open) => !open)
      } else if (key === 'b') {
        if (isEditableElement(document.activeElement) || (event.target instanceof Element && isEditableElement(event.target))) return
        event.preventDefault()
        setCollapsed((prev) => !prev)
      } else if (key === ',') {
        event.preventDefault()
        setDrawerAt(null)
        setPaletteOpen(false)
        void navigate('/settings')
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [navigate, setCollapsed])

  // Close the drawer when the window becomes wide enough for the permanent sidebar, so no invisible overlay is left behind.
  useEffect(() => {
    const query = window.matchMedia(wide)
    const onChange = () => {
      if (query.matches) setDrawerAt(null)
    }
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  const sidebarControl = useMemo(
    () => ({ collapsed: sidebarCollapsed, expand: () => setCollapsed(false) }),
    [sidebarCollapsed, setCollapsed],
  )

  // Open one inbox event stream on entering the console; messages sent to you, the "needs you" summary and every inbox view rely on it for
  // live updates.
  useInboxLive()
  useDesktopNavigation()
  return (
    <SidebarContext.Provider value={sidebarControl}>
      <div className="riso-paper flex min-h-screen text-ink nav:h-screen">
      {!sidebarCollapsed && (
        <div className="sticky top-0 h-screen w-[228px] shrink-0 self-start max-nav:hidden">
          <Sidebar
            scope={scope}
            projects={list}
            projectsError={projects.error}
            onCollapse={() => setCollapsed(true)}
            onOpenPalette={() => setPaletteOpen(true)}
          />
        </div>
      )}
      <DialogPrimitive.Root open={drawerOpen} onOpenChange={(open) => setDrawerAt(open ? location.key : null)}>
        <DialogPrimitive.Portal>
          <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-black/40 nav:hidden [[data-skin=plate]_&]:bg-ink/30" />
          <DialogPrimitive.Content
            aria-describedby={undefined}
            className="fixed inset-y-0 left-0 z-50 w-[min(248px,86vw)] shadow-dialog outline-none nav:hidden [[data-skin=plate]_&]:shadow-[6px_0_0_var(--riso-pink)]"
          >
            <DialogPrimitive.Title className="sr-only">{text.nav}</DialogPrimitive.Title>
            <Sidebar
              scope={scope}
              projects={list}
              projectsError={projects.error}
              onClose={() => setDrawerAt(null)}
              onOpenPalette={() => {
                setDrawerAt(null)
                setPaletteOpen(true)
              }}
            />
          </DialogPrimitive.Content>
        </DialogPrimitive.Portal>
      </DialogPrimitive.Root>
      <div
        data-sidebar-collapsed={sidebarCollapsed ? 'true' : undefined}
        className="riso-grain relative flex min-w-0 flex-1 flex-col nav:min-h-0"
      >
        <header
          className={cn(
            'riso-halftone sticky top-0 z-30 flex h-12 shrink-0 items-center gap-3 border-b border-sidebar-border bg-sidebar-bg px-4 text-sidebar-fg nav:hidden [[data-skin=plate]_&]:border-b-0',
            desktop && 'window-drag pl-[84px]',
          )}
        >
          <button
            type="button"
            aria-label={text.menu}
            aria-expanded={drawerOpen}
            onClick={() => setDrawerAt(location.key)}
            className="inline-flex size-9 items-center justify-center rounded-ticket border border-hairline-strong bg-surface-control text-sidebar-fg outline-none hover:bg-sidebar-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]"
          >
            <NavIcon name="menu" className="[stroke-width:2.6]" />
          </button>
          <BrandWordmark />
          <Link
            to="/settings"
            aria-label={text.items.settings}
            title={`${text.items.settings} (${text.settingsShortcut})`}
            className="ml-auto inline-flex size-8 items-center justify-center rounded-ticket border border-sidebar-border bg-surface-control text-sidebar-fg transition-colors outline-none hover:bg-sidebar-hover focus-visible:shadow-focus [[data-skin=plate]_&]:border-white/35 [[data-skin=plate]_&]:bg-white/15 [[data-skin=plate]_&]:text-white"
          >
            <Settings className="size-4" strokeWidth={1.8} />
          </Link>
        </header>
        <ConnectionBanner />
        <main className="min-h-0 flex-1">
          <Outlet />
        </main>
      </div>
      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} scope={scope} projects={list} />
      <Toasts />
      </div>
    </SidebarContext.Provider>
  )
}
