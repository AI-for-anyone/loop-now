import { SectionToc } from '@/components/SheetSections'
import { t } from '@/i18n'
import { SECTION_KEYS, sectionId, sectionTitleId, type SectionKey } from './sections'

const titles: Record<SectionKey, string> = {
  basic: t.config.basic.title,
  work: t.config.work.title,
  prompt: t.prompts.sectionTitle,
  'local-dirs': t.localMode.directories.title,
  'model-credential': t.config.modelCredential.title,
  skills: t.config.skills.title,
  clis: t.config.clis.title,
  plugins: t.plugins.agentTitle,
  danger: t.config.danger.title,
}

// The config page's section contents: the shared SectionToc (project settings use it too), with the Delete Agent entry in red.
export function ConfigToc({ className }: { className?: string }) {
  const items = SECTION_KEYS.map((key) => ({ id: sectionId(key), titleId: sectionTitleId(key), title: titles[key], danger: key === 'danger' }))
  return <SectionToc label={t.config.toc.title} items={items} className={className} />
}
