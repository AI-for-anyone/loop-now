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
  // 选好 zip 或打包好目录后调用；返回的 Promise 失败时显示原因。
  onArchive: (archive: Blob) => Promise<unknown>
  disabled?: boolean
  // 上传按钮的尺寸与附加样式：Skills 库工具栏用 lg，抽屉里浅蓝底上的按钮用白底。
  size?: 'sm' | 'lg'
  buttonClassName?: string
  // 自定义布局：按钮组与结果提示分开摆放（工具栏放按钮，提示放在工具栏下方）。不传时按钮在上、提示在下。
  children?: (parts: { actions: ReactNode; feedback: ReactNode }) => ReactNode
}

// 上传 Skill：选择 zip，或选择目录后在浏览器中打包成 zip。按服务端上限先做预检。
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
    // 服务端忽略的 .DS_Store、__MACOSX/ 不计入上限，也不放进压缩包。
    const files = skillFiles(picked)
    if (limits && files.length > limits.maxSkillEntries) {
      setLocalError(t.skills.tooManyEntries(limits.maxSkillEntries))
      return
    }
    if (limits && totalSize(files) > limits.maxSkillArchive) {
      setLocalError(t.skills.tooLarge(limits.maxSkillArchive))
      return
    }
    // 读取与打包期间也算上传中，避免再次上传。
    setPending(true)
    let archive: Blob
    try {
      archive = await zipDirectory(files)
    } catch {
      setLocalError(t.skills.packFailed)
      setPending(false)
      return
    }
    // 服务端同时限制压缩包本身的大小，打包后的大小可能超过文件原始大小之和。
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

  // 本地预检与服务端的失败原因都用错误横幅显示；名称重复时在原因下方提示改用替换。
  const message = localError ?? (error ? describeError(error) : null)
  const feedback = message ? (
    <Alert variant="error" className="shadow-print-ink">
      <CircleAlertIcon />
      <AlertTitle>{message}</AlertTitle>
      {error instanceof ApiError && error.code === 'name_conflict' && <AlertDescription>{t.skills.nameConflictHint}</AlertDescription>}
      <AlertAction>
        <Button size="icon-sm" variant="secondary" className="bg-white text-red-text hover:bg-white/80" onClick={dismiss}>
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
