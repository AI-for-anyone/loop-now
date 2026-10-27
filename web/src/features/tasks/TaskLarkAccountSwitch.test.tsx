import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { makeTaskDetail } from '@/test/tasks'
import { defaultProject } from '@/test/api'
import { projectAccount } from '@/test/project-credentials'
import { larkUserAccount } from '@/test/lark-login'
import { larkText as copy } from '@/i18n/lark'
import { ApiError } from '@/api/client'
import { TaskLarkAccountSwitch, type TaskLarkAccountSwitchProps } from './TaskLarkAccountSwitch'

const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1]
const account = larkUserAccount()
function props(): TaskLarkAccountSwitchProps {
  return { task: detail.task, node, reviewKey: 'server-review-one', accounts: [account], eligible: true,
    members: [
      { identity: node, title: '部署提测', originalPermission: '已允许', oldBinding: '原bot V1' },
      { identity: { agentId: 'followup-agent', agentName: '结果跟进员', runtimeId: 'followup-runtime' }, title: '结果跟进', originalPermission: '已允许', oldBinding: '跟进bot V3' },
    ], onConfirm: vi.fn().mockResolvedValue(undefined), onRecover: vi.fn().mockResolvedValue(undefined) }
}
async function openAndSelect() {
  await userEvent.click(screen.getByRole('button', { name: copy.switch }))
  const dialog = screen.getByRole('alertdialog')
  await userEvent.selectOptions(within(dialog).getByRole('combobox'), account.id)
  return dialog
}
describe('task Lark switch UI, injected action only', () => {
  it('lists exact two identities and each old binding, no implicit target even if unique', async () => {
    render(<TaskLarkAccountSwitch {...props()} />)
    await userEvent.click(screen.getByRole('button', { name: copy.switch }))
    const d = screen.getByRole('alertdialog')
    expect(within(d).getByRole('combobox')).toHaveValue('')
    expect(within(d).getByRole('button', { name: copy.confirm })).toBeDisabled()
    expect(within(d).getByText(/原bot V1/)).toBeInTheDocument()
    expect(within(d).getByText(/跟进bot V3/)).toBeInTheDocument()
    expect(within(d).getByText(/followup-agent · followup-runtime/)).toBeInTheDocument()
    expect(within(d).getByText(copy.preserve)).toBeInTheDocument()
    expect(within(d).getByText(copy.followup)).toBeInTheDocument()
  })
  it('single confirmation pins chosen account/version; never calls retry or sends QA itself', async () => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} />)
    const d = await openAndSelect()
    await userEvent.click(within(d).getByRole('button', { name: copy.confirm }))
    expect(p.onConfirm).toHaveBeenCalledExactlyOnceWith(account)
    expect(p.onRecover).not.toHaveBeenCalled()
  })
  it.each(['busy runtime', 'missing original session', 'old backend unavailable'])('server rejects eligibility: %s cannot open a write', reason => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} eligible={false} disabledReason={reason} />)
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    expect(screen.getByText(reason)).toBeInTheDocument()
    expect(p.onConfirm).not.toHaveBeenCalled()
  })
  it('bot, unlogged, other-project or missing-version accounts are not usable user targets', async () => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} accounts={[
      projectAccount({ purpose: 'cli', cli: 'lark-cli', authMethod: null }), { ...account, id: 'expired', status: 'requires_login' },
      { ...account, id: 'foreign', projectId: 'another' }, projectAccount({ purpose: 'cli', cli: 'lark-cli', authMethod: 'oauth', id: 'missing-version', currentVersionId: null }),
    ]} />)
    await userEvent.click(screen.getByRole('button', { name: copy.switch }))
    expect(screen.getByText(copy.noUser)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
  })
  it('task/current node change during review preserves old selection and forbids redirect', async () => {
    const p = props(), view = render(<TaskLarkAccountSwitch {...p} />)
    await openAndSelect()
    view.rerender(<TaskLarkAccountSwitch {...p} task={{ ...p.task, currentNodeRunId: 'new-node', version: p.task.version + 1 }} />)
    expect(screen.getByText(copy.changed)).toBeInTheDocument()
    expect(screen.getByRole('combobox')).toHaveValue(account.id)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
    expect(p.onConfirm).not.toHaveBeenCalled()
  })
  it('target version or member binding review change requires explicit re-review', async () => {
    const p = props(), view = render(<TaskLarkAccountSwitch {...p} />)
    await openAndSelect()
    view.rerender(<TaskLarkAccountSwitch {...p} reviewKey="server-review-two" accounts={[{ ...account, revision: 2 }]} />)
    expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled()
    expect(screen.getByText(copy.version(account.name, account.currentVersionId!), { selector: 'p' })).toBeInTheDocument()
  })
  it('unknown even after reload is GET-only original action, never confirms again', async () => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} stage="unknown" actionId="original-action" />)
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    expect(p.onRecover).toHaveBeenCalledExactlyOnceWith('original-action')
    expect(p.onConfirm).not.toHaveBeenCalled()
  })
  it('committed binding with failed dispatch is not rolled back or reported as QA success', async () => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} stage="committed" dispatchStatus="failed" actionId="original-action" />)
    expect(screen.getByText(copy.committed)).toBeInTheDocument()
    expect(screen.getByText(copy.dispatchFailed)).toBeInTheDocument()
    expect(screen.queryByText(copy.rejected)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    expect(p.onConfirm).not.toHaveBeenCalled()
  })
  it('duplicate member or historical node cannot be used as switch target', () => {
    const p = props(); const view = render(<TaskLarkAccountSwitch {...p} members={[p.members[0], p.members[0]]} />)
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    view.rerender(<TaskLarkAccountSwitch {...p} node={detail.nodes[0]} />)
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
  })
  it('late precommit failure cannot revive a committed action; a different action remains independent', () => {
    const p = props(), view = render(<TaskLarkAccountSwitch {...p} stage="committed" actionId="one" />)
    view.rerender(<TaskLarkAccountSwitch {...p} stage="rejected" actionId="one" />)
    expect(screen.getByText(copy.committed)).toBeInTheDocument()
    expect(screen.queryByText(copy.rejected)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.switch })).toBeDisabled()
    view.rerender(<TaskLarkAccountSwitch {...p} stage="review" actionId="two" />)
    expect(screen.getByRole('button', { name: copy.switch })).toBeEnabled()
  })
  it('GET404 does not say task deleted, repeat POST or roll back an already committed binding', async () => {
    const p = props(); p.onRecover = vi.fn().mockRejectedValue(new ApiError({ status: 404, code: 'not_found' }))
    const view = render(<TaskLarkAccountSwitch {...p} stage="unknown" actionId="original" />)
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    expect(screen.getByText(copy.notFound)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    view.rerender(<TaskLarkAccountSwitch {...p} stage="committed" actionId="original" />)
    expect(screen.getByText(copy.originalUnavailable)).toBeInTheDocument()
    expect(screen.getByText(copy.committed)).toBeInTheDocument()
    expect(p.onConfirm).not.toHaveBeenCalled()
  })
  it('a real eligibility403 is retained instead of disguised as unsupported or an absent operation', () => {
    const p = props(); render(<TaskLarkAccountSwitch {...p} eligible={false} error={new ApiError({ status: 403, code: 'forbidden' })} />)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.queryByText(copy.notFound)).not.toBeInTheDocument()
  })
})
