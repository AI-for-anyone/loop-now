import type { Messages } from '..'
import { plural } from '../format'

const agentCount = (count: number) => `${count} ${plural('en-US', count, { one: 'Agent', other: 'Agents' })}`

export const projects = {
  switcher: {
    isDefault: 'Default',
    agentCount: (count: number) => agentCount(count),
  },
  empty: {
    projectNote: 'An Agent belongs to the project it was created in, for good.',
    platformTitle: 'The platform has no Agents yet.',
    platformNote: "Platform-owned Agents don't belong to any project.",
  },
  create: {
    title: 'New project',
    name: 'Project name',
    hint: 'The name appears in addresses, e.g. /p/model-access/agents.',
    submit: 'Create',
    submitting: 'Creating…',
    nameRequired: 'Enter a project name',
    nameConflict: 'A project with this name already exists. Pick another.',
  },
  notFound: {
    title: (name: string) => `Can't find project "${name}"`,
    body: 'It may have been renamed or deleted. Use the scope switcher at the top left to find other projects.',
  },
  deleted: {
    stamp: 'Deleted',
    title: (name: string) => `Project "${name}" was deleted`,
    body: 'It was deleted elsewhere; its Agents are hidden from the list.',
  },
  renamed: (from: string, to: string) => `Project "${from}" was renamed to "${to}" elsewhere; the address has been updated.`,
  renamedDismiss: 'Got it',
  overview: {
    range: 'Scope',
    all: 'All',
    projectGroup: 'Projects',
    platform: 'Platform',
    missingOption: (name: string) => `${name} (not found)`,
    notFoundTitle: (name: string) => `Can't find project "${name}"`,
    notFoundBody: 'It may have been renamed or deleted.',
    backToAll: 'Back to all',
    emptyProject: {
      title: (name: string) => `"${name}" has no Agents yet.`,
      body: "Once you create an Agent, this shows only this project's run status, usage, and issues.",
      action: (name: string) => `Go to the Agents in "${name}"`,
    },
    emptyPlatform: {
      title: 'The platform has no Agents yet.',
      body: "Once you create a platform Agent, this shows only the platform's run status, usage, and issues.",
      action: 'Go to platform Agents',
    },
    deletedProject: 'Deleted project',
  },
  toDefault: 'Go to the default project',
  settings: {
    title: 'Project settings',
    name: {
      title: 'Project name',
      id: 'Project ID',
      label: 'Project name',
      hint: "Renaming changes the addresses of the project's Agents; the old addresses stop working.",
      saved: 'Saved',
      movedTo: 'Address changed to',
    },
    manager: {
      title: 'Manager',
      intro: "Pick one of this project's Agents as its Manager. A project has at most one, and you can change it any time, even while the Agent is busy.",
      none: 'This project has no Manager yet',
      choose: 'Assign Manager',
      change: 'Change Manager',
      placeholder: 'Choose an Agent',
      filter: 'Filter Agents by name',
      noMatch: 'No matching Agents',
      clear: 'Remove Manager',
      assigned: (name: string) => `"${name}" is now the Manager`,
      cleared: 'Manager removed',
      noAgents: 'The project has no Agents yet. Create one first.',
      createAgent: 'Create an Agent',
    },
    danger: {
      title: 'Delete project',
      notes: [
        "The project and its Agents are hidden from the list, and the project's shared accounts and credentials are deleted.",
        'Working folders, sessions, and conversation history are kept.',
        'After deletion, the project name can be reused.',
        'A project can only be deleted when all its Agents are idle and it has no unfinished tasks, including sleeping ones.',
      ],
      action: 'Delete this project',
      confirmTitle: (name: string) => `Delete project "${name}"?`,
      confirmBody: (count: number) =>
        count > 0
          ? `This also deletes the project's ${agentCount(count)} and its shared accounts and credentials. Working folders, sessions, and conversation history are kept; the project name can be reused.`
          : "The project has no Agents. Its shared accounts and credentials are deleted; working folders, sessions, and conversation history are kept. The project name can be reused.",
      confirm: 'Delete project',
      notIdleTitle: "Some Agents in the project aren't idle, so the project wasn't deleted.",
      notIdleBody: "Wait for these Agents' replies and queued messages to finish, then delete the project:",
      busyList: 'Agents that are not idle',
      openChat: 'Open chat',
      openChatOf: (name: string) => `Open chat with "${name}"`,
      defaultNote: "The default project can't be deleted, but it can be renamed.",
    },
  },
} satisfies Messages['projects']
