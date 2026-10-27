// 配置页的区块，顺序即分区目录的顺序。区块与目录按这里的编号找到彼此。
export const SECTION_KEYS = ['basic', 'prompt', 'model-credential', 'skills', 'clis', 'plugins', 'danger'] as const
export type SectionKey = (typeof SECTION_KEYS)[number]

export const sectionId = (key: SectionKey) => `config-${key}`
export const sectionTitleId = (key: SectionKey) => `config-${key}-title`
