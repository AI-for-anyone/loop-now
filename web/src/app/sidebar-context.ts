import { createContext, useContext } from 'react'

export interface SidebarControl {
  collapsed: boolean
  expand: () => void
}

export const SidebarContext = createContext<SidebarControl>({
  collapsed: false,
  expand: () => {},
})

export function useSidebarControl(): SidebarControl {
  return useContext(SidebarContext)
}
