import type { ReactNode } from 'react'
import { SheetSection } from '@/components/SheetSections'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

interface SectionProps {
  id: SectionKey
  title: string
  // 标题旁的条目数，只作视觉提示；区块名称仍是标题本身。
  count?: number
  actions?: ReactNode
  // danger：删除 Agent 一区，编号与标题红色，内容放在红色斜纹的底上。
  tone?: 'default' | 'danger'
  children: ReactNode
}

// 配置页的编号分区：共用的 SheetSection（项目设置也用它），编号按 SECTION_KEYS 的顺序，与目录尺上的编号一致。
export function Section({ id, ...props }: SectionProps) {
  return <SheetSection id={sectionId(id)} titleId={sectionTitleId(id)} index={SECTION_KEYS.indexOf(id)} {...props} />
}
