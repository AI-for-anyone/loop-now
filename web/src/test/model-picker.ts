import { within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

// Picks a model in the model dropdown (combobox): opens the dropdown and clicks the option for the ID.
export async function chooseModel(user: UserEvent, scope: HTMLElement, modelId: string) {
  await user.click(await within(scope).findByRole('combobox', { name: '模型' }))
  const listbox = await within(document.body).findByRole('listbox')
  const option = within(listbox)
    .getAllByRole('option')
    .find((o) => o.getAttribute('aria-label')?.includes(modelId))
  if (!option) throw new Error(`下拉中没有模型 ${modelId}`)
  await user.click(option)
}

// Opens the model dropdown and returns the full description of each option.
export async function modelOptions(user: UserEvent, scope: HTMLElement): Promise<string[]> {
  await user.click(await within(scope).findByRole('combobox', { name: '模型' }))
  const listbox = await within(document.body).findByRole('listbox')
  return within(listbox)
    .getAllByRole('option')
    .map((o) => o.getAttribute('aria-label') ?? '')
}
