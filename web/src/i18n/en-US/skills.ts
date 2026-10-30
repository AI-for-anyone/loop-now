import type { Messages } from '..'
import { plural } from '../format'
import { formatBytes, formatTime } from './format'

export const skills = {
  title: 'Skills library',
  empty: 'No templates in the Skills library yet.',
  uploadZip: 'Upload zip',
  uploadDir: 'Upload folder',
  create: 'Create online',
  count: (count: number) => `${count} ${plural('en-US', count, { one: 'template', other: 'templates' })}`,
  files: (count: number) => `${count} ${plural('en-US', count, { one: 'file', other: 'files' })}`,
  fileList: 'Files',
  skillMd: 'SKILL.md',
  nameConflictHint: 'The library already has a template with this name. To update it, open that template and use Replace.',
  replace: 'Replace',
  replaceTitle: (name: string) => `Replace template "${name}"?`,
  replaceBody: 'The template is replaced entirely by the new upload. Skill copies already given to Agents are not affected.',
  deleteTitle: (name: string) => `Delete template "${name}"?`,
  deleteBody: 'The template is removed from the library. Skill copies already given to Agents are not affected.',
  newTitle: 'New Skill',
  tooLarge: (limit: number) => `Over the archive limit (${formatBytes(limit)})`,
  tooManyEntries: (limit: number) => `Too many files (limit ${limit})`,
  packFailed: "Couldn't read or pack the folder. Make sure every file in it is readable, then try again.",
  skeleton: '---\nname: my-skill\ndescription: One sentence on what this Skill does and when to use it\n---\n\n# my-skill\n\n',
  detailTitle: 'Template details',
  notFound: "This template doesn't exist or was deleted.",
  updated: (time: string) => `Updated ${formatTime(time)}`,
  submitCreate: 'Create',
} satisfies Messages['skills']
