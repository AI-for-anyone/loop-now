import type { SectionRulerAnchor } from '@/components/PrecisionRuler'
import { t } from '@/i18n'
import { sectionId, sectionTitleId, TAB_SECTIONS, type ConfigTab, type SectionKey } from './sections'

const titles: Record<SectionKey, string> = {
  basic: t.config.basic.title,
  'model-credential': t.config.modelCredential.title,
  permissions: t.permissions.title,
  prompt: t.prompts.sectionTitle,
  work: t.config.work.title,
  'local-dirs': t.localMode.directories.title,
  skills: t.config.skills.title,
  clis: t.config.clis.title,
  plugins: t.plugins.agentTitle,
  danger: t.config.danger.title,
}

// Section anchors for the active config sub-tab, fed to PageBody's floating vernier PageRuler.
export function tabRulerSections(tab: ConfigTab): SectionRulerAnchor[] {
  return TAB_SECTIONS[tab].map((key) => ({
    id: sectionId(key),
    titleId: sectionTitleId(key),
    title: titles[key],
    danger: key === 'danger',
  }))
}
