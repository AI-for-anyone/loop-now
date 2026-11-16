import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { DirtyContext } from '@/features/prompts/draft-navigation'
import { makeConfig, webhookSchema } from '@/test/plugins'
import { renderWithQuery } from '@/test/render'
import { ConfigForm, type ConfigPayload } from './SchemaForm'

const guard = ({ children }: { children: ReactNode }) => <DirtyContext.Provider value={() => {}}>{children}</DirtyContext.Provider>
const conflict = () => new ApiError({ status: 412, code: 'precondition_failed', serverMessage: 'revision mismatch' })

function renderForm(props: Partial<Parameters<typeof ConfigForm>[0]> = {}) {
  const onSave = vi.fn(async (payload: ConfigPayload) => makeConfig({ values: payload.values as never, secretsSet: ['secret'], revision: payload.expectedRevision + 1 }))
  const reload = vi.fn(async () => makeConfig({ values: { url: 'https://other.example/hook' } as never, secretsSet: ['secret'], revision: 7 }))
  const utils = renderWithQuery(<ConfigForm formId="test" schema={webhookSchema} config={makeConfig({ values: { url: 'https://a.example/hook', retries: 5 } as never, secretsSet: ['secret'], revision: 3 })} onSave={onSave} reload={reload} {...props} />, undefined, { wrapper: guard })
  return { ...utils, onSave, reload }
}

describe('Saving the plugin config form', () => {
  it('a secret already set only shows "Set"; Replace shows an input, and saving sends only the new value with the current revision', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    expect(screen.getByText('Set')).toBeInTheDocument()
    expect(screen.queryByLabelText('签名密钥', { selector: 'input' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: 'Replace 签名密钥' }))
    await user.type(screen.getByLabelText('签名密钥'), 'new-secret')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave).toHaveBeenCalledWith({ values: { url: 'https://a.example/hook', retries: 5 }, secrets: { secret: 'new-secret' }, expectedRevision: 3 })
    expect(await screen.findByText('Saved')).toBeInTheDocument()
  })

  it('when local validation fails, sends no request and marks the reason under each field', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    await user.clear(screen.getByLabelText('回调地址'))
    await user.type(screen.getByLabelText('回调地址'), 'hooks.example.com')
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '20')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(onSave).not.toHaveBeenCalled()
    expect(screen.getByLabelText('回调地址')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('Enter a full URL, e.g. https://…')).toBeInTheDocument()
    expect(screen.getByText('Must be at most 10')).toBeInTheDocument()
  })

  it('412: says it changed elsewhere; loading the latest discards the changes, and overwriting saves again on the latest revision', async () => {
    const user = userEvent.setup()
    const { onSave, reload } = renderForm()
    onSave.mockRejectedValueOnce(conflict())
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '2')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    const group = await screen.findByRole('group', { name: 'Config changed elsewhere' })
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.click(within(group).getByRole('button', { name: 'Overwrite with my changes' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    expect(reload).toHaveBeenCalledTimes(1)
    expect(onSave).toHaveBeenLastCalledWith({ values: { url: 'https://a.example/hook', retries: 2 }, expectedRevision: 7 })
    expect(screen.queryByRole('group', { name: 'Config changed elsewhere' })).toBeNull()
  })

  it('choosing to load the latest after 412 replaces the inputs with the new server values', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    onSave.mockRejectedValueOnce(conflict())
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '2')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await user.click(await screen.findByRole('button', { name: 'Load the latest, discard my changes' }))
    await waitFor(() => expect(screen.getByLabelText('回调地址')).toHaveValue('https://other.example/hook'))
    expect(screen.getByLabelText('重试上限')).toHaveValue('')
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('422 field errors are marked on their fields', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    onSave.mockRejectedValueOnce(new ApiError({ status: 422, code: 'validation_failed', serverMessage: 'invalid', fields: [{ field: 'values.url', message: 'The address is unreachable' }] }))
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '1')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('The address is unreachable')).toBeInTheDocument()
    expect(screen.getByLabelText('回调地址')).toHaveAttribute('aria-invalid', 'true')
  })

  it('project overrides list only x-scope: project fields and show the global value; empty ones are not sent', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm({
      mode: 'project',
      config: makeConfig({ revision: 0 }),
      globalConfig: makeConfig({ values: { url: 'https://hooks.example.com/loop-now' } as never, revision: 3 }),
    })
    expect(screen.queryByLabelText('重试上限')).toBeNull()
    expect(screen.queryByText('签名密钥')).toBeNull()
    expect(screen.getByText(/Global value: https:\/\/hooks.example.com\/loop-now/)).toBeInTheDocument()
    await user.type(screen.getByLabelText('回调地址'), 'https://hooks.example.com/propanda')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ values: { url: 'https://hooks.example.com/propanda' }, expectedRevision: 0 }))
  })
})
