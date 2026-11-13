import type { Messages } from '..'

export const localMode = {
  settings: {
    on: 'On',
    off: 'Off',
    title: 'Local mode',
    intro: 'Lets Agents use what is already on this computer. Each of the four has its own switch: the first three are on by default, and "Use local logins" stays off until you turn it on.',
    effect: '"Bind local directories", "Use local CLIs" and "Use local logins" take effect when an Agent handles its next message; a reply in progress is not affected. "Use local git identity" takes effect from the next pull.',
    items: {
      directories: {
        label: 'Bind local directories',
        description: 'Agents are told about the local directories listed in project settings and Agent config, and can read and write the real files in them. When off, the lists are kept but no Agent is told about them.',
      },
      git: {
        label: 'Use local git identity',
        description: 'For "Pull from Git", HTTPS addresses can use the git credentials saved on this computer (such as in the keychain), and SSH addresses can use the keys in ssh-agent; SSH keys under ~/.ssh work either way. Only the pull itself is affected.',
      },
      cli: {
        label: 'Use local CLIs',
        description: 'Agents can run commands installed on this computer, but those commands still sign in with the accounts configured in Loop Now, not with your logins in your own terminal. To use those, also turn on "Use local logins".',
      },
      logins: {
        label: 'Use local logins',
        description: 'Agents use your logins for the tools on this computer, which is the same as acting as you, and all Agents share them. CLIs with an account configured for the Agent in Loop Now still use that account. Independent of "Use local CLIs": with only this one on, only commands Agents could already run can use these logins.',
      },
    },
    loginsConfirm: {
      title: 'Turn on "Use local logins"?',
      body: 'From the next message an Agent handles:',
      points: [
        'Agents use your logins for the tools on this computer, which is the same as acting as you.',
        'All Agents share these logins, including Agents in tasks.',
        'CLIs with an account configured for the Agent in Loop Now still use that account.',
      ],
      confirm: 'Turn on',
    },
  },
  directories: {
    title: 'Local directories',
    intro: {
      project: 'Tells every Agent in the project that it can work in these local directories, including Agents in tasks.',
      agent: "Tells this Agent that it can work in these local directories; the project's directories apply to it as well.",
      platform: 'Tells this Agent that it can work in these local directories.',
    },
    cautionTitle: 'Before you use this',
    cautions: [
      'Changes land on the real files: what an Agent changes in these directories is written straight to your files, in tasks too, without going through a task copy.',
      "This list tells Agents where to work; it doesn't isolate access. Agents can read and write directories that aren't on it just the same.",
    ],
    off: '"Bind local directories" is off in global settings: the list is still saved, but no Agent is told about it.',
    openSettings: 'Open global settings',
    list: 'Local directories',
    empty: 'No local directories yet.',
    add: 'Add directory',
    full: (max: number) => `You can list up to ${max} directories.`,
    edit: 'Edit',
    editLabel: (path: string) => `Edit ${path}`,
    remove: 'Remove',
    removeLabel: (path: string) => `Remove ${path}`,
    removeTitle: (path: string) => `Remove "${path}" from the list?`,
    removeBody: "This only removes it from the list. The directory and its files aren't deleted.",
    status: {
      missing: "Doesn't exist",
      not_directory: 'Not a directory',
      inaccessible: "Can't be read",
    },
    statusHint: "Agents aren't told about this one for now.",
    dialog: {
      addTitle: 'Add local directory',
      editTitle: 'Edit local directory',
      path: 'Absolute path',
      pathHint: "The full path on this computer, such as /Users/me/code/app, or C:\\Users\\me\\code\\app on Windows. It can be saved even if the directory doesn't exist yet.",
      note: 'Note (optional)',
      noteHint: 'Given to Agents along with the path, for example what the directory holds.',
    },
    conflict: 'The list was just changed elsewhere and the latest version has been loaded. Check it, then save again.',
    gone: 'This entry was removed elsewhere.',
    inherited: {
      title: (project: string) => `From project "${project}"`,
      hint: 'Applies to every Agent in the project; read-only here.',
      empty: 'The project has no local directories yet.',
      edit: 'Edit in project settings',
    },
    own: "This Agent's own directories",
  },
} satisfies Messages['localMode']
