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
  it('保存原生 JSON，并在修订冲突时保留草稿核对', async () => {
    let settings = { modelsJson: '{"providers":{}}', revision: 1 }
    const writes: unknown[] = []
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json(settings)),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers: [] })),
      http.put(apiUrl('/model-settings'), async ({ request }) => {
        const body = await request.json() as { modelsJson: string; expectedRevision: number }
        writes.push(body)
        if (body.expectedRevision !== settings.revision) return HttpResponse.json(errorBody('precondition_failed', '配置已修改'), { status: 412 })
        settings = { modelsJson: body.modelsJson, revision: settings.revision + 1 }
        return HttpResponse.json(settings)
      }),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{ "providers": {} }')
    await user.click(screen.getByRole('button', { name: '保存模型配置' }))
    await screen.findByText('已保存')
    expect(settings.revision).toBe(2)

    settings = { modelsJson: '{"providers":{"other":{}}}', revision: 3 }
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')
    await user.click(screen.getByRole('button', { name: '保存模型配置' }))
    await screen.findByRole('group', { name: '核对模型配置差异' })
    expect(editor.textContent?.trim()).toBe('{ "providers": {} }')
    expect(editor.textContent).not.toBe('{ "providers": {} }')
    expect(screen.getByText('服务器文本 · 修订 3')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '已核对修订 3，保留草稿继续编辑' }))
    await user.click(screen.getByRole('button', { name: '保存模型配置' }))
    await waitFor(() => expect(settings.revision).toBe(4))
    expect(writes.map(value => (value as { expectedRevision: number }).expectedRevision)).toEqual([1, 2, 3])
  })

  it('即时定位 JSON 语法错误并阻止无效保存', async () => {
    const writes: unknown[] = []
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      http.put(apiUrl('/model-settings'), async ({ request }) => { writes.push(await request.json()); return HttpResponse.json({}) }),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{\n  "providers": {\n')
    const location = await screen.findByRole('button', { name: /第 3 行，第 1 列/ })
    expect(screen.getByRole('alert', { name: 'JSON 语法错误' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '保存模型配置' })).toBeDisabled()
    await user.click(location)
    expect(editor).toHaveFocus()
    expect(writes).toEqual([])
  })

  it('在编辑器旁展示服务端校验错误且保留草稿', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      http.put(apiUrl('/model-settings'), () => HttpResponse.json(errorBody('validation_failed', '保存失败', { fields: [{ field: 'modelsJson', message: 'Pi 拒绝了模型配置' }] }), { status: 422 })),
    )
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await replaceJSON(user, editor, '{"providers":{"draft":{}}}')
    await user.click(screen.getByRole('button', { name: '保存模型配置' }))
    await screen.findByText('配置未保存，草稿已保留。')
    await user.click(screen.getByText('查看校验详情'))
    expect(screen.getByText(/Pi 拒绝了模型配置/)).toBeInTheDocument()
    expect(editor.textContent).toBe('{"providers":{"draft":{}}}')
  })

  it('从完整示例合并 Provider，同名时必须明确确认', async () => {
    server.use(http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{"my-openai":{"baseUrl":"keep"},"other":{"keep":true}}}', revision: 1 })))
    const user = userEvent.setup()
    renderCard()
    const editor = await screen.findByLabelText('models.json')
    await user.click(screen.getByRole('button', { name: '示例与字段说明' }))
    const example = await screen.findByRole('region', { name: '最小 OpenAI 兼容 Provider' })
    await user.click(within(example).getByRole('button', { name: '合并到草稿' }))
    expect(editor.textContent).toContain('keep')
    await user.click(within(example).getByRole('button', { name: '替换同名 Provider 并插入' }))
    await screen.findByText('已插入“最小 OpenAI 兼容 Provider”，请检查后保存。')
    expect(editor.textContent).not.toContain('"baseUrl": "keep"')
    expect(editor.textContent).toContain('my-openai')
    expect(editor.textContent).toContain('other')
    expect(example).not.toBeInTheDocument()
  })
})
