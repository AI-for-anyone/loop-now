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

describe('插件配置表单的保存', () => {
  it('已设置的密钥只显示“已设置”；点更换出现输入框，保存时只发新值，并带上当前版本', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    expect(screen.getByText('已设置')).toBeInTheDocument()
    expect(screen.queryByLabelText('签名密钥', { selector: 'input' })).toBeNull()
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '更换 签名密钥' }))
    await user.type(screen.getByLabelText('签名密钥'), 'new-secret')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1))
    expect(onSave).toHaveBeenCalledWith({ values: { url: 'https://a.example/hook', retries: 5 }, secrets: { secret: 'new-secret' }, expectedRevision: 3 })
    expect(await screen.findByText('已保存')).toBeInTheDocument()
  })

  it('本地校验不过时不发请求，在对应的项下标出原因', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    await user.clear(screen.getByLabelText('回调地址'))
    await user.type(screen.getByLabelText('回调地址'), 'hooks.example.com')
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '20')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(onSave).not.toHaveBeenCalled()
    expect(screen.getByLabelText('回调地址')).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText('请填完整地址，例如 https://…')).toBeInTheDocument()
    expect(screen.getByText('不能大于 10')).toBeInTheDocument()
  })

  it('412：说明别处改过；载入最新会放弃修改，用我的修改覆盖则按最新版本重新保存', async () => {
    const user = userEvent.setup()
    const { onSave, reload } = renderForm()
    onSave.mockRejectedValueOnce(conflict())
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '2')
    await user.click(screen.getByRole('button', { name: '保存' }))
    const group = await screen.findByRole('group', { name: '配置已在别处修改' })
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    await user.click(within(group).getByRole('button', { name: '用我的修改覆盖' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2))
    expect(reload).toHaveBeenCalledTimes(1)
    expect(onSave).toHaveBeenLastCalledWith({ values: { url: 'https://a.example/hook', retries: 2 }, expectedRevision: 7 })
    expect(screen.queryByRole('group', { name: '配置已在别处修改' })).toBeNull()
  })

  it('412 后选载入最新：输入框换成服务端的新值', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    onSave.mockRejectedValueOnce(conflict())
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '2')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await user.click(await screen.findByRole('button', { name: '载入最新，放弃我的修改' }))
    await waitFor(() => expect(screen.getByLabelText('回调地址')).toHaveValue('https://other.example/hook'))
    expect(screen.getByLabelText('重试上限')).toHaveValue('')
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('422 的字段错误标到对应的项', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm()
    onSave.mockRejectedValueOnce(new ApiError({ status: 422, code: 'validation_failed', serverMessage: 'invalid', fields: [{ field: 'values.url', message: '地址不可达' }] }))
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '1')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('地址不可达')).toBeInTheDocument()
    expect(screen.getByLabelText('回调地址')).toHaveAttribute('aria-invalid', 'true')
  })

  it('项目覆盖只列 x-scope: project 的项，写出全局值；留空的不发送', async () => {
    const user = userEvent.setup()
    const { onSave } = renderForm({
      mode: 'project',
      config: makeConfig({ revision: 0 }),
      globalConfig: makeConfig({ values: { url: 'https://hooks.example.com/loop-now' } as never, revision: 3 }),
    })
    expect(screen.queryByLabelText('重试上限')).toBeNull()
    expect(screen.queryByText('签名密钥')).toBeNull()
    expect(screen.getByText(/全局值：https:\/\/hooks.example.com\/loop-now/)).toBeInTheDocument()
    await user.type(screen.getByLabelText('回调地址'), 'https://hooks.example.com/propanda')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(onSave).toHaveBeenCalledWith({ values: { url: 'https://hooks.example.com/propanda' }, expectedRevision: 0 }))
  })
})
