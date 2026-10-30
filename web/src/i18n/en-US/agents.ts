import type { Messages } from '..'

export const agents = {
  create: 'New Agent',
  search: { label: 'Search Agents', placeholder: 'Search by name or address', noMatch: 'No matching Agents' },
  empty: 'No Agents yet.',
  notFound: "This Agent doesn't exist or was deleted.",
  busy: 'Busy',
  idle: 'Idle',
  sessionMissing: 'Session lost',
  tabs: { chat: 'Chat', config: 'Config', folder: 'Folder', inbox: 'Inbox' },
  address: {
    label: 'Address',
    missing: "This Agent has no address yet: its name doesn't meet the naming rules. Rename it to get one. Until then, use its internal ID to send it Inbox messages.",
  },
  selectHint: 'Pick an Agent in the sidebar.',
  manager: 'Manager',
  managerTitle: 'Project Manager',
} satisfies Messages['agents']

export const createAgent = {
  title: 'New Agent',
  submit: 'Create',
  modelRequired: 'Choose a model',
  nameRequired: 'Enter a display name',
  nameConflict: 'The project already has an Agent with this name. Pick another.',
  platformNameConflict: 'The platform already has an Agent with this name. Pick another.',
  nameRules: {
    separator: 'Names can\'t contain "/" or "#"',
    atStart: 'Names can\'t start with "@"',
    whitespace: "Names can't start or end with whitespace",
    control: "Names can't contain control characters",
    uuid: "Names can't look like a UUID",
    tooLong: 'Names can be at most 100 characters',
  },
} satisfies Messages['createAgent']
