import { useRef, useState, type ChangeEvent, type ReactNode } from 'react'
import { CircleAlertIcon, XIcon } from 'lucide-react'
import { Alert, AlertAction, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ApiError } from '@/api/client'
import { useContextInfo } from '@/api/queries'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { pickedFiles } from '@/lib/upload'
import { skillFiles, totalSize, zipDirectory } from '@/lib/zip'

interface SkillUploadProps {
  // Called once a zip is picked or a folder is packed; when the returned Promise rejects, the reason is shown.
  onArchive: (archive: Blob) => Promise<unknown>
  disabled?: boolean
  // Size and extra classes of the upload buttons: the Skills library toolbar uses lg; buttons on the light blue background in
  // the drawer use a white background.
  size?: 'sm' | 'lg'
  buttonClassName?: string
  // Custom layout: place the button group and the result message separately (buttons in the toolbar, the message below the
  // toolbar). When omitted, the buttons go on top and the message below.
  children?: (parts: { actions: ReactNode; feedback: ReactNode }) => ReactNode
}

// Upload a Skill: pick a zip, or pick a folder and pack it into a zip in the browser. Checked against the server limits first.
export function SkillUpload({ onArchive, disabled, size = 'sm', buttonClassName, children }: SkillUploadProps) {
  const context = useContextInfo()
  const limits = context.data?.limits
  const zipInput = useRef<HTMLInputElement>(null)
  const dirInput = useRef<HTMLInputElement>(null)
  const [error, setError] = useState<unknown>(null)
  const [localError, setLocalError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const send = async (archive: Blob) => {
    setPending(true)
    try {
      await onArchive(archive)
    } catch (e) {
      setError(e)
    } finally {
      setPending(false)
    }
  }

  const onZip = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    setError(null)
    setLocalError(null)
    if (!file) return
    if (limits && file.size > limits.maxSkillArchive) {
      setLocalError(t.skills.tooLarge(limits.maxSkillArchive))
      return
    }
    void send(file)
  }

  const onDir = async (event: ChangeEvent<HTMLInputElement>) => {
    const picked = event.target.files ? pickedFiles(event.target.files) : []
    event.target.value = ''
    setError(null)
    setLocalError(null)
    if (picked.length === 0) return
    // .DS_Store and __MACOSX/, which the server ignores, don't count toward the limits and aren't put into the archive.
    const files = skillFiles(picked)
    if (limits && files.length > limits.maxSkillEntries) {
      setLocalError(t.skills.tooManyEntries(limits.maxSkillEntries))
      return
    }
    if (limits && totalSize(files) > limits.maxSkillArchive) {
      setLocalError(t.skills.tooLarge(limits.maxSkillArchive))
      return
    }
    // Reading and packing also count as uploading, to prevent a second upload.
    setPending(true)
    let archive: Blob
    try {
      archive = await zipDirectory(files)
    } catch {
      setLocalError(t.skills.packFailed)
      setPending(false)
      return
    }
    // The server also limits the size of the archive itself, and the packed size can exceed the sum of the original file sizes.
    if (limits && archive.size > limits.maxSkillArchive) {
      setLocalError(t.skills.tooLarge(limits.maxSkillArchive))
      setPending(false)
      return
    }
    await send(archive)
  }

  const dismiss = () => {
    setError(null)
    setLocalError(null)
  }

  const actions = (
    <>
      <Button size={size} variant="secondary" className={buttonClassName} disabled={disabled || pending} onClick={() => dirInput.current?.click()}>
        {t.skills.uploadDir}
      </Button>
      <Button size={size} variant="secondary" className={buttonClassName} disabled={disabled || pending} onClick={() => zipInput.current?.click()}>
        {t.skills.uploadZip}
      </Button>
      {pending && <span className="self-center text-[13px] font-bold text-ink-500">{t.common.uploading}</span>}
    </>
  )

  // Failure reasons from both the local check and the server are shown in an error banner; on a name conflict, a hint below
  // the reason suggests using replace instead.
  const message = localError ?? (error ? describeError(error) : null)
  const feedback = message ? (
    <Alert variant="error" className="shadow-print-ink">
      <CircleAlertIcon />
      <AlertTitle>{message}</AlertTitle>
      {error instanceof ApiError && error.code === 'name_conflict' && <AlertDescription>{t.skills.nameConflictHint}</AlertDescription>}
      <AlertAction>
        <Button size="icon-sm" variant="secondary" className="bg-surface-control text-red-text hover:bg-surface-hover [[data-skin=plate]_&]:bg-white [[data-skin=plate]_&]:hover:bg-white/80" onClick={dismiss}>
          <XIcon />
          <span className="sr-only">{t.common.close}</span>
        </Button>
      </AlertAction>
    </Alert>
  ) : null

  return (
    <>
      <input ref={zipInput} type="file" accept=".zip,application/zip" hidden aria-label={t.skills.uploadZip} onChange={onZip} />
      <input
        ref={dirInput}
        type="file"
        multiple
        hidden
        aria-label={t.skills.uploadDir}
        onChange={(e) => void onDir(e)}
        {...({ webkitdirectory: '' } as Record<string, string>)}
      />
      {children ? (
        children({ actions, feedback })
      ) : (
        <div className="grid gap-3">
          <div className="flex flex-wrap gap-2.5">{actions}</div>
          {feedback}
        </div>
      )}
    </>
  )
}
