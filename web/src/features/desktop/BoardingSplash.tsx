import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { cn } from '@/lib/utils'
import { hasInsetTitleBar } from './desktop'

// Timing constants (ms):
// - Cold-start handoff from status.html: brief settle then smooth fade-out.
// - Reopening a hidden/minimized window: holds long enough for one crisp 90-degree turn of the golden square, then fades out.
const INITIAL_HOLD_MS = 280
const REOPEN_HOLD_MS = 880
const FADE_MS = 220
const REDUCED_HOLD_MS = 220
const REDUCED_FADE_MS = 140

type Phase = 'hidden' | 'hold' | 'fade'

function prefersReducedMotion(): boolean {
  try {
    return typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  } catch {
    return false
  }
}

function hasDesktopBoarding(): boolean {
  return typeof window !== 'undefined' && typeof window.loopNowDesktop?.onBoarding === 'function'
}

// Centered minimalist desktop boarding splash: renders the obsidian-silver-loop emblem with a rotating top-right golden rounded square
// and no text. Smoothly hands off from desktop/static/status.html on launch and replays briefly whenever the desktop window is reopened,
// keeping the live console tree, drafts and event streams intact underneath.
export function BoardingSplash() {
  const [phase, setPhase] = useState<Phase>(() => (hasDesktopBoarding() ? 'hold' : 'hidden'))
  const [cycle, setCycle] = useState(0)
  const holdTimer = useRef<number | null>(null)
  const fadeTimer = useRef<number | null>(null)
  const rawId = useId()
  const prefix = rawId.replace(/[^a-zA-Z0-9_-]/g, '')

  const clearTimers = useCallback(() => {
    if (holdTimer.current !== null) {
      window.clearTimeout(holdTimer.current)
      holdTimer.current = null
    }
    if (fadeTimer.current !== null) {
      window.clearTimeout(fadeTimer.current)
      fadeTimer.current = null
    }
  }, [])

  const scheduleDismiss = useCallback(
    (holdMs: number) => {
      clearTimers()
      const reduced = prefersReducedMotion()
      const effectiveHold = reduced ? Math.min(holdMs, REDUCED_HOLD_MS) : holdMs
      const effectiveFade = reduced ? REDUCED_FADE_MS : FADE_MS
      holdTimer.current = window.setTimeout(() => {
        holdTimer.current = null
        setPhase('fade')
        fadeTimer.current = window.setTimeout(() => {
          fadeTimer.current = null
          setPhase('hidden')
        }, effectiveFade)
      }, effectiveHold)
    },
    [clearTimers],
  )

  useEffect(() => {
    const onBoarding = window.loopNowDesktop?.onBoarding
    if (typeof onBoarding !== 'function') return
    scheduleDismiss(INITIAL_HOLD_MS)
    const unsubscribe = onBoarding(() => {
      setCycle((prev) => prev + 1)
      setPhase('hold')
      scheduleDismiss(REOPEN_HOLD_MS)
    })
    return () => {
      clearTimers()
      unsubscribe()
    }
  }, [clearTimers, scheduleDismiss])

  if (phase === 'hidden') return null

  const id = (name: string) => `${prefix}-${name}`
  const inset = hasInsetTitleBar()

  return (
    <div
      data-desktop-boarding={phase}
      role="status"
      aria-label="Loop Now"
      className={cn(
        'fixed inset-0 z-[100] grid place-items-center bg-surface-canvas',
        phase === 'fade' ? 'pointer-events-none opacity-0 transition-opacity duration-[220ms] ease-out' : 'opacity-100',
      )}
    >
      {inset && <div className="window-drag fixed inset-x-0 top-0 h-12" aria-hidden="true" />}
      <svg
        key={cycle}
        viewBox="0 0 64 64"
        aria-hidden="true"
        className="size-[72px] drop-shadow-[0_12px_28px_rgba(0,0,0,0.14)] dark:drop-shadow-[0_14px_34px_rgba(0,0,0,0.65)]"
      >
        <defs>
          <clipPath id={id('sq')}>
            <path d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z" />
          </clipPath>
          <clipPath id={id('lc')}>
            <path d="M37 15H27C19 15 15 19 15 27V37C15 45 19 49 27 49H37C45 49 49 45 49 37V29H39V36C39 38 38 39 36 39H28C26 39 25 38 25 36V28C25 26 26 25 28 25H37Z" />
          </clipPath>
          <linearGradient id={id('bg')} x1="50%" y1="0%" x2="50%" y2="100%">
            <stop offset="0%" stopColor="#2A2D34" />
            <stop offset="18%" stopColor="#1A1C22" />
            <stop offset="52%" stopColor="#0E1014" />
            <stop offset="100%" stopColor="#06070A" />
          </linearGradient>
          <radialGradient id={id('oh')} cx="50%" cy="2%" r="72%" fx="44%" fy="0%">
            <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.22" />
            <stop offset="32%" stopColor="#DCE4F2" stopOpacity="0.08" />
            <stop offset="65%" stopColor="#FFFFFF" stopOpacity="0.015" />
            <stop offset="100%" stopColor="#FFFFFF" stopOpacity="0" />
          </radialGradient>
          <radialGradient id={id('rf')} cx="50%" cy="104%" r="60%">
            <stop offset="0%" stopColor="#C8D6EE" stopOpacity="0.16" />
            <stop offset="45%" stopColor="#7B8BA6" stopOpacity="0.045" />
            <stop offset="100%" stopColor="#000000" stopOpacity="0" />
          </radialGradient>
          <radialGradient id={id('dp')} cx="50%" cy="50%" r="54%">
            <stop offset="68%" stopColor="#000000" stopOpacity="0" />
            <stop offset="100%" stopColor="#000000" stopOpacity="0.34" />
          </radialGradient>
          <radialGradient id={id('ga')} cx="45" cy="19" r="16" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#F8CE64" stopOpacity="0.28" />
            <stop offset="45%" stopColor="#C98A1E" stopOpacity="0.09" />
            <stop offset="100%" stopColor="#C98A1E" stopOpacity="0" />
          </radialGradient>
          <linearGradient id={id('rd')} x1="6%" y1="2%" x2="94%" y2="98%">
            <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.72" />
            <stop offset="18%" stopColor="#E8EEF8" stopOpacity="0.24" />
            <stop offset="48%" stopColor="#FFFFFF" stopOpacity="0.07" />
            <stop offset="80%" stopColor="#D4DFEE" stopOpacity="0.18" />
            <stop offset="100%" stopColor="#FFFFFF" stopOpacity="0.46" />
          </linearGradient>
          <linearGradient id={id('rs')} x1="50%" y1="0%" x2="50%" y2="100%">
            <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.24" />
            <stop offset="28%" stopColor="#FFFFFF" stopOpacity="0.03" />
            <stop offset="75%" stopColor="#FFFFFF" stopOpacity="0.02" />
            <stop offset="100%" stopColor="#C8D8F0" stopOpacity="0.16" />
          </linearGradient>
          <linearGradient id={id('sv')} x1="20%" y1="6%" x2="78%" y2="94%">
            <stop offset="0%" stopColor="#FFFFFF" />
            <stop offset="22%" stopColor="#EEF1F6" />
            <stop offset="52%" stopColor="#CFD6E0" />
            <stop offset="76%" stopColor="#B0B8C5" />
            <stop offset="92%" stopColor="#C6CDD8" />
            <stop offset="100%" stopColor="#9CA4B2" />
          </linearGradient>
          <radialGradient id={id('sb')} cx="24" cy="20" r="24" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.62" />
            <stop offset="45%" stopColor="#FFFFFF" stopOpacity="0.14" />
            <stop offset="100%" stopColor="#FFFFFF" stopOpacity="0" />
          </radialGradient>
          <linearGradient id={id('svb')} x1="16%" y1="6%" x2="84%" y2="94%">
            <stop offset="0%" stopColor="#FFFFFF" stopOpacity="0.98" />
            <stop offset="40%" stopColor="#FFFFFF" stopOpacity="0.28" />
            <stop offset="72%" stopColor="#353B46" stopOpacity="0.34" />
            <stop offset="100%" stopColor="#EEF3FA" stopOpacity="0.48" />
          </linearGradient>
          <linearGradient id={id('gd')} x1="18%" y1="4%" x2="82%" y2="96%">
            <stop offset="0%" stopColor="#FFF5C8" />
            <stop offset="22%" stopColor="#F9D876" />
            <stop offset="55%" stopColor="#DFAA39" />
            <stop offset="84%" stopColor="#B57E1B" />
            <stop offset="100%" stopColor="#CE992F" />
          </linearGradient>
          <radialGradient id={id('gb')} cx="43.5" cy="16.5" r="7.5" gradientUnits="userSpaceOnUse">
            <stop offset="0%" stopColor="#FFFDF0" stopOpacity="0.72" />
            <stop offset="50%" stopColor="#FFE694" stopOpacity="0.18" />
            <stop offset="100%" stopColor="#FFE694" stopOpacity="0" />
          </radialGradient>
          <linearGradient id={id('gdb')} x1="12%" y1="6%" x2="88%" y2="94%">
            <stop offset="0%" stopColor="#FFFDF2" stopOpacity="0.96" />
            <stop offset="42%" stopColor="#FFE89E" stopOpacity="0.32" />
            <stop offset="76%" stopColor="#5E3A05" stopOpacity="0.42" />
            <stop offset="100%" stopColor="#F7CF65" stopOpacity="0.56" />
          </linearGradient>
          <filter id={id('ms')} x="-25%" y="-25%" width="150%" height="165%">
            <feDropShadow dx="0" dy="2.1" stdDeviation="2.2" floodColor="#000000" floodOpacity="0.72" />
            <feDropShadow dx="0" dy="0.5" stdDeviation="0.55" floodColor="#000000" floodOpacity="0.55" />
          </filter>
          <filter id={id('gs')} x="-55%" y="-55%" width="210%" height="210%">
            <feDropShadow dx="0" dy="1.8" stdDeviation="1.9" floodColor="#000000" floodOpacity="0.68" />
          </filter>
        </defs>
        <g clipPath={`url(#${id('sq')})`}>
          <path
            d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z"
            fill={`url(#${id('bg')})`}
          />
          <path
            d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z"
            fill={`url(#${id('dp')})`}
          />
          <path
            d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z"
            fill={`url(#${id('oh')})`}
          />
          <path
            d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z"
            fill={`url(#${id('rf')})`}
          />
          <circle cx="45" cy="19" r="16" fill={`url(#${id('ga')})`} />
          <g filter={`url(#${id('ms')})`}>
            <path
              d="M37 15H27C19 15 15 19 15 27V37C15 45 19 49 27 49H37C45 49 49 45 49 37V29H39V36C39 38 38 39 36 39H28C26 39 25 38 25 36V28C25 26 26 25 28 25H37Z"
              fill={`url(#${id('sv')})`}
            />
            <path
              d="M37 15H27C19 15 15 19 15 27V37C15 45 19 49 27 49H37C45 49 49 45 49 37V29H39V36C39 38 38 39 36 39H28C26 39 25 38 25 36V28C25 26 26 25 28 25H37Z"
              fill={`url(#${id('sb')})`}
            />
            <path
              d="M37 15H27C19 15 15 19 15 27V37C15 45 19 49 27 49H37C45 49 49 45 49 37V29H39V36C39 38 38 39 36 39H28C26 39 25 38 25 36V28C25 26 26 25 28 25H37Z"
              fill="none"
              stroke={`url(#${id('svb')})`}
              strokeWidth="1.2"
              clipPath={`url(#${id('lc')})`}
            />
          </g>
          <g filter={`url(#${id('gs')})`}>
            <g className="boarding-gold-square">
              <rect x="40" y="14" width="10" height="10" rx="3" fill={`url(#${id('gd')})`} />
              <rect x="40" y="14" width="10" height="10" rx="3" fill={`url(#${id('gb')})`} />
              <rect x="40.5" y="14.5" width="9" height="9" rx="2.5" fill="none" stroke={`url(#${id('gdb')})`} strokeWidth="0.9" />
            </g>
          </g>
          <path
            d="M32 0.75C50 0.75 55.9 0.75 59.6 4.4C63.25 8.1 63.25 14 63.25 32C63.25 50 63.25 55.9 59.6 59.6C55.9 63.25 50 63.25 32 63.25C14 63.25 8.1 63.25 4.4 59.6C0.75 55.9 0.75 50 0.75 32C0.75 14 0.75 8.1 4.4 4.4C8.1 0.75 14 0.75 32 0.75Z"
            fill="none"
            stroke={`url(#${id('rs')})`}
            strokeWidth="2.6"
          />
          <path
            d="M32 0.75C50 0.75 55.9 0.75 59.6 4.4C63.25 8.1 63.25 14 63.25 32C63.25 50 63.25 55.9 59.6 59.6C55.9 63.25 50 63.25 32 63.25C14 63.25 8.1 63.25 4.4 59.6C0.75 55.9 0.75 50 0.75 32C0.75 14 0.75 8.1 4.4 4.4C8.1 0.75 14 0.75 32 0.75Z"
            fill="none"
            stroke={`url(#${id('rd')})`}
            strokeWidth="0.95"
          />
          <path
            d="M32 0C50.4 0 56.4 0 60.2 3.8C64 7.6 64 13.6 64 32C64 50.4 64 56.4 60.2 60.2C56.4 64 50.4 64 32 64C13.6 64 7.6 64 3.8 60.2C0 56.4 0 50.4 0 32C0 13.6 0 7.6 3.8 3.8C7.6 0 13.6 0 32 0Z"
            fill="none"
            stroke="#000000"
            strokeOpacity="0.52"
            strokeWidth="0.8"
          />
        </g>
      </svg>
    </div>
  )
}
