import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

export function QuestionSummary({ question }: { question: Schemas['InboxQuestion'] }) {
  return <section aria-label={t.inbox.question.title} className="mt-3 grid min-w-0 gap-2 border-t border-ink/20 pt-3">
    <Badge>{t.inbox.question.state[question.state]}</Badge>
    <ul className="grid gap-1">{question.options.map(option => <li className="break-words" key={option.id}>{option.label}</li>)}</ul>
    <p className="text-xs">{t.inbox.question.expires(question.expiresAt)}</p>
    {question.answer && <div className="grid gap-1"><h4 className="font-bold">{t.inbox.question.result}</h4>{question.answer.optionLabel && <p className="break-words">{question.answer.optionLabel}</p>}<p className="break-words">{question.answer.textPreview}</p><p className="text-xs">{question.answer.by.name} · {t.inbox.time(question.answer.at)}</p></div>}
  </section>
}
