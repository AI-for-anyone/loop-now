import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

export function InboxNotice({ notice }: { notice: Schemas['InboxNotice'] }) {
  return <div className="mt-2 grid gap-1"><Badge>{t.inbox.noticeType[notice.type]}</Badge><p className="text-sm break-words">{notice.reason}</p></div>
}
