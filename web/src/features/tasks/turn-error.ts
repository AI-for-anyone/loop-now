import { t } from '@/i18n'
import { runErrorText } from '@/features/chat/run-status-text'

// The error line of a task turn (a message in the node chat, or a run in the task run record). The turn's errorMessage is the
// server's fixed sentence for its errorCode and says nothing more than the code, so a known code shows only its copy. The server's
// text is the fallback for an unknown or missing code: English for new turns, Chinese for turns stored before the switch.
// runtimeName: the runtime the turn ran on (TaskMessage.runtime), named in a runtime failure; "the runtime" when it isn't known.
export function turnErrorText(code: string | null, message: string | null, runtimeName: string | null = null): string {
  const known = code ? turnErrorLabel(code, runtimeName) : null
  if (known) return known
  return message ? `${t.tasks.messageError} · ${message}` : t.tasks.messageError
}

function turnErrorLabel(code: string, runtimeName: string | null): string | null {
  if (code === 'user_stopped') return t.chat.status.user_stopped
  if (code in t.chat.runError || code in t.chat.runtimeError) return runErrorText(code, runtimeName)
  return t.tasks.turnErrors[code] ?? null
}
