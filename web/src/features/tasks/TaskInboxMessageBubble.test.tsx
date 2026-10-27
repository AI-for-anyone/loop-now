import { screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { makeInboxMessage, consoleParty, externalParty, makeIdentity } from '@/test/inbox'
import type { Schemas } from '@/api/client'
import { TaskInboxMessageBubble } from './TaskInboxMessageBubble'

const agent: Schemas['InboxParticipant'] = { kind: 'agent', id: '00000000-0000-7000-8000-000000000104', address: '项目/原任务Agent#任务', name: '原任务Agent', deleted: false, pluginId: null, channelId: null }
it('真实Inbox未读状态只取readAt，不把接收当已读/已回复，不开放回答表单', () => {
  const message = makeInboxMessage({ from: consoleParty, to: agent, text: '只读真实Inbox正文' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('已接收，Agent 未读')).toBeVisible(); expect(screen.queryByText('Agent 已读取')).toBeNull(); expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.queryByText('Inbox 回复')).toBeNull()
})
it('已读只表示真实读取，原sender与显式inReplyTo确定Inbox回复来源', () => {
  const message = makeInboxMessage({ from: agent, to: consoleParty, text: '真正通过Inbox回复', inReplyTo: 'original-message-id' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('原任务Agent')).toBeVisible(); expect(screen.getByText('Inbox 回复')).toBeVisible(); expect(screen.getByText(/original-message-id/)).toBeVisible(); expect(screen.queryByText('Agent 工作输出')).toBeNull()
})
it('已删除Agent保留原地址显示，不借当前配置猜sender', () => {
  const message = makeInboxMessage({ from: { ...agent, name: '已删除 Agent', deleted: true }, to: consoleParty, text: '保留的历史回复', inReplyTo: 'original-id' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('已删除 Agent')).toBeVisible(); expect(screen.getByText('原任务Agent#任务')).toBeVisible()
})
it('任务外部Inbox仍保留完整正文和图片，不使用普通聊天摘要', () => {
  const text = '完整的任务消息。'.repeat(30)
  const message = makeInboxMessage({ from: externalParty(makeIdentity()), to: agent, text, images: [{ id: '00000000-0000-7000-8000-000000000105', mimeType: 'image/png', size: 4, width: 1, height: 1 }] })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByRole('note')).toHaveTextContent(text)
  expect(screen.getByRole('list', { name: '消息图片' }).querySelector('img')).toHaveAttribute('src', expect.stringContaining('/message-images/00000000-0000-7000-8000-000000000105'))
})
