import type { ReactNode } from 'react'
import { SheetSection } from '@/components/SheetSections'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

interface SectionProps {
  id: SectionKey
  title: string
  // Item count next to the title, a visual hint only; the section's name is still just the title.
  count?: number
  actions?: ReactNode
  // danger: the Delete Agent section; number and title in red, content on a red diagonal-stripe background.
  tone?: 'default' | 'danger'
  children: ReactNode
}

// A numbered section of the config page: the shared SheetSection (project settings use it too); numbers follow the order of SECTION_KEYS
// and match the numbers on the section ruler.
export function Section({ id, ...props }: SectionProps) {
  return <SheetSection id={sectionId(id)} titleId={sectionTitleId(id)} index={SECTION_KEYS.indexOf(id)} {...props} />
}
