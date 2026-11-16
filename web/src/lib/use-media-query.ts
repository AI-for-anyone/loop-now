import { useSyncExternalStore } from 'react'

// Whether the current window matches a media query (e.g. a narrow screen), re-rendering when the window changes. In the test environment
// matchMedia never matches.
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const list = window.matchMedia(query)
      list.addEventListener('change', onChange)
      return () => list.removeEventListener('change', onChange)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}

// Narrow screens, where the sidebar folds into the drawer and stubs are hidden (matches Tailwind's max-nav).
export const narrowQuery = '(width < 56.3125rem)'
