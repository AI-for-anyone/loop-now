import { within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

// 思考等级是一组分段按钮（单选组，名称“思考等级”）：每一段是一个单选项，名称就是段上的字。
export const thinkingPicker = (container: HTMLElement) => within(container).getByRole('radiogroup', { name: '思考等级' })

// 各段的名称，按顺序。
export const thinkingOptions = (container: HTMLElement) => within(thinkingPicker(container)).getAllByRole('radio').map((radio) => radio.textContent)

// 当前选中的一段的名称；没有选中的为 null。
export const thinkingChoice = (container: HTMLElement) =>
  within(thinkingPicker(container)).getAllByRole('radio').find((radio) => radio.getAttribute('aria-checked') === 'true')?.textContent ?? null

// 点选某一段。
export async function chooseThinking(user: UserEvent, container: HTMLElement, label: string) {
  await user.click(within(thinkingPicker(container)).getByRole('radio', { name: label }))
}
