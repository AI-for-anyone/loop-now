import { t } from '@/i18n'
import { runErrorText } from '@/features/chat/run-status-text'

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
  // A task turn does not say which runtime it was on, so a runtime failure reads "the runtime".
  if (code in t.chat.runError || code in t.chat.runtimeError) return runErrorText(code)
  return t.tasks.turnErrors[code] ?? null
}
