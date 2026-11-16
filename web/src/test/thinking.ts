import { within } from '@testing-library/react'
import type { UserEvent } from '@testing-library/user-event'

// The thinking level is a group of segmented buttons (a radio group named “思考等级”
// (Thinking level)): each segment is a radio option, named by the text on it.
export const thinkingPicker = (container: HTMLElement) => within(container).getByRole('radiogroup', { name: '思考等级' })

// Names of the segments, in order.
export const thinkingOptions = (container: HTMLElement) => within(thinkingPicker(container)).getAllByRole('radio').map((radio) => radio.textContent)

// Name of the currently selected segment; null when none is selected.
export const thinkingChoice = (container: HTMLElement) =>
  within(thinkingPicker(container)).getAllByRole('radio').find((radio) => radio.getAttribute('aria-checked') === 'true')?.textContent ?? null

// Clicks one segment.
export async function chooseThinking(user: UserEvent, container: HTMLElement, label: string) {
  await user.click(within(thinkingPicker(container)).getByRole('radio', { name: label }))
}
