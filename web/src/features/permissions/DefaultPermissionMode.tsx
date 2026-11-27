import { CheckIcon } from 'lucide-react'
import { useState } from 'react'
import { usePermissionSettings, usePutPermissionSettings } from '@/api/permissions'
import { ErrorAlert } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { PermissionModePicker } from './PermissionModePicker'

// Settings: the mode new and imported Agents start with (Full access until someone chooses otherwise); existing Agents keep theirs.
export function DefaultPermissionMode({ labelledBy }: { labelledBy: string }) {
  const settings = usePermissionSettings()
  const put = usePutPermissionSettings()
  const [saved, setSaved] = useState(false)
  if (!settings.data) return settings.isError ? <ErrorAlert error={settings.error} /> : <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[13.5px] text-ink-700">{t.permissions.defaultHint}</p>
      <PermissionModePicker
        value={settings.data.defaultMode}
        labelledBy={labelledBy}
        disabled={put.isPending}
        onChange={(mode) => {
          setSaved(false)
          put.mutate({ defaultMode: mode }, { onSuccess: () => setSaved(true) })
        }}
      />
      {saved && (
        <span role="status" className="inline-flex items-center gap-1 text-[12.5px] font-semibold text-green-text">
          <CheckIcon aria-hidden="true" className="size-3.5" strokeWidth={2.8} />
          {t.permissions.saved}
        </span>
      )}
      <ErrorAlert error={put.error} />
    </div>
  )
}
