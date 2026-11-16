// The config page's sections; this order is the order of the section contents. Sections and contents find each other by the numbers here.
export const SECTION_KEYS = ['basic', 'prompt', 'local-dirs', 'model-credential', 'skills', 'clis', 'plugins', 'danger'] as const
export type SectionKey = (typeof SECTION_KEYS)[number]

export const sectionId = (key: SectionKey) => `config-${key}`
export const sectionTitleId = (key: SectionKey) => `config-${key}-title`
