import { SectionToc } from '@/components/SheetSections'
import { t } from '@/i18n'
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

// 配置页的分区目录：共用的 SectionToc（项目设置也用它），删除 Agent 一项是红色。
export function ConfigToc({ className }: { className?: string }) {
  const items = SECTION_KEYS.map((key) => ({ id: sectionId(key), titleId: sectionTitleId(key), title: titles[key], danger: key === 'danger' }))
  return <SectionToc label={t.config.toc.title} items={items} className={className} />
}
