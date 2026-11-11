import type { Messages } from '..'

export const runtime = {
  names: { pi: 'Pi', 'claude-code': 'Claude Code' },
  badge: 'Claude Code',
  badgeTitle: 'Runtime: Claude Code on this computer',
  providerOption: (name: string, count: number) => `${name} (this computer, ${count} ${count === 1 ? 'model' : 'models'})`,
  providerNote: "Uses the Claude Code installed on this computer and your login there. The platform doesn't store login details.",
  differences: {
    title: 'Running on Claude Code',
    home: 'It runs with your home directory and logins on this computer: gh, git and other commands it runs use your own logins.',
    commands: 'It runs commands without asking first, the same as Pi Agents.',
    plugins: "Plugins that only contain Pi extensions don't apply to it; plugin skills and MCP tools work.",
    piCommands: 'There are no /pi: commands.',
  },
  switch: {
    title: 'Switch runtime?',
    body: (from: string, to: string) =>
      `The runtime changes from ${from} to ${to}. The Agent starts a new session: the current session is kept but not carried over, and pending sleeps in it are cancelled.`,
    confirm: 'Switch and save',
    notIdle: 'The Agent is replying. Switch after this reply finishes.',
  },
  account: "This Agent uses the Claude Code on this computer and your login there. It needs no account on the platform, and the platform doesn't store login details.",
} satisfies Messages['runtime']
