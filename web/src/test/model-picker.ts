import { screen, within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'
import { t } from '@/i18n'

// The runner picker: a rail of runtimes (tabs), the models of the runtime looked at (a tree, providers as rows under a runtime with
// several), and the thinking level.
export const runnerPicker = (scope: HTMLElement) => within(scope).getByRole('group', { name: t.runtime.picker.label })
export const findRunnerPicker = (scope: HTMLElement) => within(scope).findByRole('group', { name: t.runtime.picker.label })

// The rail's tab for a runtime, by its name (the tab's name goes on with its state).
export async function runnerTab(scope: HTMLElement, runtime: string) {
  const tabs = await within(scope).findAllByRole('tab')
  const tab = tabs.find((candidate) => candidate.getAttribute('aria-label')?.startsWith(`${runtime},`))
  if (!tab) throw new Error(`No runtime ${runtime} in the picker`)
  return tab
}

// Looks at a runtime in the rail.
export async function chooseRunner(user: UserEvent, scope: HTMLElement, runtime: string) {
  await user.click(await runnerTab(scope, runtime))
}

// Opens a provider's row under a runtime with several providers (a no-op when it is open already, or when the runtime has one provider and
// lists its models directly).
export async function openProvider(user: UserEvent, scope: HTMLElement, providerId: string) {
  const rows = await within(scope).findAllByRole('treeitem')
  const row = rows.find((candidate) => candidate.hasAttribute('aria-expanded') && candidate.getAttribute('aria-label')?.split(',')[0] === providerId)
  if (row?.getAttribute('aria-expanded') === 'false') await user.click(row.firstElementChild as HTMLElement)
}

// Chooses a model: looks at the runtime first when given, opens the provider when given, and clicks the model's row (its name contains the
// model id).
export async function chooseModel(user: UserEvent, scope: HTMLElement, modelId: string, where: { runtime?: string; provider?: string } = {}) {
  if (where.runtime) await chooseRunner(user, scope, where.runtime)
  if (where.provider) await openProvider(user, scope, where.provider)
  const rows = await within(scope).findAllByRole('treeitem')
  const row = rows.find((candidate) => candidate.getAttribute('aria-level') !== null && !candidate.hasAttribute('aria-expanded') && candidate.getAttribute('aria-label')?.includes(modelId))
  if (!row) throw new Error(`No model ${modelId} in the picker`)
  await user.click(row)
}

// The full description of each model row shown in the picker.
export async function modelOptions(scope: HTMLElement): Promise<string[]> {
  await within(scope).findAllByRole('treeitem')
  return within(scope)
    .getAllByRole('treeitem')
    .filter((row) => !row.hasAttribute('aria-expanded'))
    .map((row) => row.getAttribute('aria-label') ?? '')
}

// The search box over the model list.
export const modelSearch = (scope: HTMLElement) => within(scope).getByRole('combobox', { name: new RegExp(`${t.runtime.picker.search}|${t.runtime.picker.searchGrouped}`) })

// Anything shown outside the scope (no popover content here): kept for callers that look in the whole page.
export const page = () => screen
