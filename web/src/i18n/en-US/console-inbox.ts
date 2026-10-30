import type { Messages } from '..'
import { plural } from '../format'

const count = (n: number, one: string, other: string) => `${n} ${plural('en-US', n, { one, other })}`

export const consoleInbox = {
  toYou: 'For you',
  unread: 'Unread',
  question: 'Question',
  openUntil: (time: string) => `Open until ${time}`,
  chosen: (time: string, label: string) => `You chose "${label}" at ${time}`,
  answeredByText: (time: string, text: string) => `You replied at ${time}: ${text}`,
  answeredBy: (name: string, time: string) => `${name} answered at ${time}`,
  replyToQuestion: 'Reply to this question',
  cannotAnswer: "This question can't be answered right now. Check the task's latest state.",
  locateQuestion: 'Answer in the conversation',
  replying: (name: string) => `Replying to ${name}'s question`,
  cancelReply: 'Cancel replying to this question',
  replyPlaceholder: 'Type your answer to this question…',
  replyNoImages: "You can't attach images when answering a question. Remove them first.",
  answered: 'You answered this question.',
  conversation: 'Conversation with you',
  conversationEmpty: 'No messages with you yet.',
  conversationIntro: 'Messages between this external identity and you. Replies go after the latest message by default.',
  newMessage: 'New message',
  newMessageLabel: (name: string) => `New message to ${name}`,
  newMessageHint: 'A new message starts a new thread.',
  cancel: 'Cancel',
  pendingTitle: 'Needs you',
  pendingCount: (n: number) => count(n, 'item', 'items'),
  pendingKind: { question: 'Question', unread: 'New message' },
  inTask: (title: string) => `Task "${title}"`,
  taskFallback: 'Task instance',
  answer: 'Answer',
  view: 'View',
  badge: (questions: number, unread: number) =>
    [questions ? count(questions, 'question', 'questions') : '', unread ? count(unread, 'new message', 'new messages') : ''].filter(Boolean).join(', '),
  badgeLabel: (summary: string) => `Needs your reply: ${summary}`,
} satisfies Messages['consoleInbox']
