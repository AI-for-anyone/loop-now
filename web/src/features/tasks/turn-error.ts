import { t } from '@/i18n'

// The error line of a task turn (a message in the node chat, or a run in the task run record). The turn's errorMessage is the
// server's fixed sentence for its errorCode and says nothing more than the code, so a known code shows only its copy. The server's
// text is the fallback for an unknown or missing code: English for new turns, Chinese for turns stored before the switch.
export function turnErrorText(code: string | null, message: string | null): string {
  const known = code ? turnErrorLabel(code) : null
  if (known) return known
  return message ? `${t.tasks.messageError} · ${message}` : t.tasks.messageError
}

function turnErrorLabel(code: string): string | null {
  if (code === 'user_stopped') return t.chat.status.user_stopped
  if (code in t.chat.runError) return t.chat.runError[code as keyof typeof t.chat.runError]
  return t.tasks.turnErrors[code] ?? null
}
