import { screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { makeInboxMessage, consoleParty, externalParty, makeIdentity } from '@/test/inbox'
import type { Schemas } from '@/api/client'
import { TaskInboxMessageBubble } from './TaskInboxMessageBubble'

const agent: Schemas['InboxParticipant'] = { kind: 'agent', id: '00000000-0000-7000-8000-000000000104', address: 'project/Original task Agent#task', name: 'Original task Agent', deleted: false, pluginId: null, channelId: null }
it('the real Inbox unread state comes only from readAt, received is not treated as read or replied, and no answer form opens', () => {
  const message = makeInboxMessage({ from: consoleParty, to: agent, text: 'read-only real Inbox text' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('Received; unread by the Agent')).toBeVisible(); expect(screen.queryByText('Read by the Agent')).toBeNull(); expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.queryByText('Inbox reply')).toBeNull()
})
it('read means only an actual read, and the original sender and an explicit inReplyTo determine where an Inbox reply comes from', () => {
  const message = makeInboxMessage({ from: agent, to: consoleParty, text: 'actually replied through the Inbox', inReplyTo: 'original-message-id' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('Original task Agent')).toBeVisible(); expect(screen.getByText('Inbox reply')).toBeVisible(); expect(screen.getByText(/original-message-id/)).toBeVisible(); expect(screen.queryByText('Agent work output')).toBeNull()
})
it('a deleted Agent keeps showing its original address, without guessing the sender from the current config', () => {
  const message = makeInboxMessage({ from: { ...agent, name: 'Deleted Agent', deleted: true }, to: consoleParty, text: 'kept history reply', inReplyTo: 'original-id' })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByText('Deleted Agent')).toBeVisible(); expect(screen.getByText('Original task Agent#task')).toBeVisible()
})
it('an external task Inbox message keeps its full text and images, without the regular chat summary', () => {
  const text = 'The full task message.'.repeat(30)
  const message = makeInboxMessage({ from: externalParty(makeIdentity()), to: agent, text, images: [{ id: '00000000-0000-7000-8000-000000000105', mimeType: 'image/png', size: 4, width: 1, height: 1 }] })
  renderWithQuery(<TaskInboxMessageBubble message={message} />)
  expect(screen.getByRole('note')).toHaveTextContent(text)
  expect(screen.getByRole('list', { name: 'Message images' }).querySelector('img')).toHaveAttribute('src', expect.stringContaining('/message-images/00000000-0000-7000-8000-000000000105'))
})
