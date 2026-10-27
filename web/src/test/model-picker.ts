import { within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

// 在模型下拉（combobox）中选择一个模型：打开下拉，点击 ID 对应的选项。
export async function chooseModel(user: UserEvent, scope: HTMLElement, modelId: string) {
  await user.click(await within(scope).findByRole('combobox', { name: '模型' }))
  const listbox = await within(document.body).findByRole('listbox')
  const option = within(listbox)
    .getAllByRole('option')
    .find((o) => o.getAttribute('aria-label')?.includes(modelId))
  if (!option) throw new Error(`下拉中没有模型 ${modelId}`)
  await user.click(option)
}

// 打开模型下拉，返回各选项的完整说明。
export async function modelOptions(user: UserEvent, scope: HTMLElement): Promise<string[]> {
  await user.click(await within(scope).findByRole('combobox', { name: '模型' }))
  const listbox = await within(document.body).findByRole('listbox')
  return within(listbox)
    .getAllByRole('option')
    .map((o) => o.getAttribute('aria-label') ?? '')
}
