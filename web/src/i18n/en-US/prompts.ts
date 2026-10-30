import type { Messages } from '..'

export const prompts = {
  sectionTitle: 'Prompt',
  projectTitle: 'Project prompt',
  agentTitle: 'Agent prompt',
  saveProject: 'Save project prompt',
  saveAgent: 'Save Agent prompt',
  inherited: 'Project prompt (read-only)',
  source: (name: string) => `Project prompt · inherited from ${name} · read-only`,
  savedConfiguration: 'Currently saved config; running tasks keep the snapshot taken when they started.',
  editProject: 'Edit in project settings',
  empty: '(empty)',
  projectHint:
    "Applies to every Agent in this project, together with each Agent's own prompt. Project rules win on conflict; this is a convention in the prompt, not an enforced permission.",
  agentHint: "Adds this Agent's duties and ways of working on top of the project rules. Saving here doesn't change the project prompt.",
  platformHint: "Platform Agents use only their own prompt; they don't inherit project rules.",
  platformEffect: 'The current reply keeps the old config; the next dispatched message uses the new Agent prompt.',
  effect:
    'The current reply keeps the old config; the next dispatched message uses the new one. Tasks already running keep the two-level snapshot from when they started for later nodes, conversations, and retries; only new tasks use the new config.',
  baseline: (version: number) => `Editing from version ${version}`,
  backgroundUpdated: 'The server has a newer version. Your draft and editing baseline are kept; saving still checks the original version.',
  conflictTitle: 'Review prompt differences',
  conflictBody:
    "Another page changed this content, so nothing was saved. Compare the server's text with your draft, pick the version you reviewed to keep editing, and save again; the version is checked again.",
  reading: "Reading the server's latest content…",
  readFailed: "Couldn't read the latest content. Your draft and editing baseline are kept; read again before reviewing.",
  readAgain: 'Read the latest content again',
  serverVersion: (version: number) => `Server text · version ${version}`,
  draft: 'My draft',
  newerThanReview: 'The server changed again while you were reviewing. The version shown here is kept; saving may still report a conflict.',
  adopt: (version: number) => `Reviewed version ${version}; keep editing my draft`,
} satisfies Messages['prompts']
