import type { Messages } from '..'

export const runtime = {
  names: { pi: 'Pi', 'claude-code': 'Claude Code' },
  badgeTitle: (name: string) => `Runtime: ${name} on this computer`,
  providerOption: (name: string, count: number) => `${name} (this computer, ${count} ${count === 1 ? 'model' : 'models'})`,
  providerNote: (name: string) => `Uses the ${name} installed on this computer and your login there. The platform doesn't store login details.`,
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
  status: {
    title: 'Agent runtimes',
    intro: {
      bundled: (name: string) => `${name} ships with the platform.`,
      local: (name: string) => `${name} uses its installation and your login on this computer; the platform doesn't read or store its login.`,
      installed: (name: string) => `${name} uses its installation on this computer.`,
      byRuntime: { 'claude-code': "Claude Code uses the claude installed on this computer and your login there; the platform doesn't read or store its login." },
      separator: ' ',
    },
    refresh: 'Check again',
    refreshing: 'Checking…',
    available: 'Available',
    unavailable: 'Unavailable',
    login: { logged_in: 'Logged in', logged_out: 'Logged out', unknown: 'Login status unknown' },
    version: 'Version',
    path: 'Path',
    account: 'Login',
    subscription: (type: string) => `${type} subscription`,
    checkedAt: (time: string) => `Checked ${time}`,
    problems: {
      not_found: (name: string) => `${name} wasn't found on this computer: install it and make sure it runs in a terminal.`,
      version_failed: (name: string) => `Reading the version of ${name} failed: make sure it runs normally in a terminal.`,
      unavailable: (name: string) => `${name} isn't available on this computer right now.`,
      logged_out: (name: string) => `${name} isn't logged in on this computer: log in to it in a terminal there.`,
    },
    problemsByRuntime: {
      'claude-code': {
        not_found: "claude wasn't found on this computer: install Claude Code and make sure claude runs in a terminal.",
        version_failed: 'Running claude --version failed: make sure claude runs normally in a terminal.',
        logged_out: "Claude Code isn't logged in on this computer: run claude in a terminal there and log in.",
      },
    },
    unknownLogin: "The login status can't be determined; if a run fails, the error says why.",
    pickerWarning: (problem: string) => `${problem} Until then, Agents using it will fail to run.`,
  },
  account: (name: string) => `This Agent uses the ${name} on this computer and your login there. It needs no account on the platform, and the platform doesn't store login details.`,
} satisfies Messages['runtime']
