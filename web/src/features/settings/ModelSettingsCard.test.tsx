import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { DirtyContext } from '@/features/prompts/draft-navigation'
import { ModelSettingsCard } from '@/features/settings/ModelSettingsCard'
import { apiUrl, errorBody } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'

async function replaceJSON(user: ReturnType<typeof userEvent.setup>, editor: HTMLElement, text: string) {
  await user.click(editor)
  await user.keyboard('{Control>}a{/Control}')
  await user.paste(text)
}

function renderCard() {
  return renderWithQuery(<DirtyContext.Provider value={() => {}}><ModelSettingsCard /></DirtyContext.Provider>)
}

describe('ModelSettingsCard', () => {
  it('saves the raw JSON, and on a revision conflict keeps the draft for review', async () => {
    let settings = { modelsJson: '{"providers":{}}', revision: 1 }
    const writes: unknown[] = []
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json(settings)),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers: [] })),
      http.put(apiUrl('/model-settings'), async ({ request }) => {
        const body = await request.json() as { modelsJson: string; expectedRevision: number }
        writes.push(body)
        if (body.expectedRevision !== settings.revision) return HttpResponse.json(errorBody('precondition_failed', 'The configuration has changed'), { status: 412 })
        settings = { modelsJson: body.modelsJson, revision: settings.revision + 1 }
        return HttpResponse.json(settings)
      }),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{ "providers": {} }')
    await user.click(screen.getByRole('button', { name: 'Save model settings' }))
    await screen.findByText('Saved')
    expect(settings.revision).toBe(2)

    settings = { modelsJson: '{"providers":{"other":{}}}', revision: 3 }
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')
    await user.click(screen.getByRole('button', { name: 'Save model settings' }))
    await screen.findByRole('group', { name: 'Review model configuration differences' })
    expect(editor.textContent?.trim()).toBe('{ "providers": {} }')
    expect(editor.textContent).not.toBe('{ "providers": {} }')
    expect(screen.getByText('Server text · revision 3')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Revision 3 reviewed; keep my draft and continue editing' }))
    await user.click(screen.getByRole('button', { name: 'Save model settings' }))
    await waitFor(() => expect(settings.revision).toBe(4))
    expect(writes.map(value => (value as { expectedRevision: number }).expectedRevision)).toEqual([1, 2, 3])
  })

  it('points to JSON syntax errors right away and blocks saving invalid JSON', async () => {
    const writes: unknown[] = []
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      http.put(apiUrl('/model-settings'), async ({ request }) => { writes.push(await request.json()); return HttpResponse.json({}) }),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{\n  "providers": {\n')
    const location = await screen.findByRole('button', { name: /Line 3, column 1/ })
    expect(screen.getByRole('alert', { name: 'JSON syntax errors' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Save model settings' })).toBeDisabled()
    await user.click(location)
    expect(editor).toHaveFocus()
    expect(writes).toEqual([])
  })

  it('shows server validation errors next to the editor and keeps the draft', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      http.put(apiUrl('/model-settings'), () => HttpResponse.json(errorBody('validation_failed', 'modelsJson: Pi rejected the model configuration', { fields: [{ field: 'modelsJson', message: 'Pi rejected the model configuration' }] }), { status: 422 })),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{"providers":{"draft":{}}}')
    await user.click(screen.getByRole('button', { name: 'Save model settings' }))
    await screen.findByText('Configuration was not saved. Your draft is preserved.')
    await user.click(screen.getByText('View validation details'))
    expect(screen.getByText(/Pi rejected the model configuration/)).toBeInTheDocument()
    expect(editor.textContent).toBe('{"providers":{"draft":{}}}')
  })

  it('merges a provider from a full example, and a provider with the same name needs explicit confirmation', async () => {
    server.use(http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{"my-openai":{"baseUrl":"keep"},"other":{"keep":true}}}', revision: 1 })))
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await user.click(screen.getByRole('button', { name: 'Examples and field guide' }))
    const example = await screen.findByRole('region', { name: 'Minimal OpenAI-compatible provider' })
    await user.click(within(example).getByRole('button', { name: 'Merge into draft' }))
    expect(editor.textContent).toContain('keep')
    await user.click(within(example).getByRole('button', { name: 'Replace matching provider and insert' }))
    await screen.findByText('Inserted “Minimal OpenAI-compatible provider”. Review it before saving.')
    expect(editor.textContent).not.toContain('"baseUrl": "keep"')
    expect(editor.textContent).toContain('my-openai')
    expect(editor.textContent).toContain('other')
    expect(example).not.toBeInTheDocument()
  })
})
