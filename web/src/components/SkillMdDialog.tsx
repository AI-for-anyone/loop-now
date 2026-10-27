import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { t } from '@/i18n'
import { ErrorAlert } from './ErrorAlert'

interface SkillMdDialogProps {
  title: string
  // 尚在加载时为 undefined。
  initial: string | undefined
  loadError?: unknown
  onSave: (content: string) => Promise<unknown>
  onClose: () => void
}

// 在线编辑 SKILL.md（Agent 的 Skill 副本）：保存失败时保留已编辑的内容。编辑器为白底等宽字，按钮在右下。
export function SkillMdDialog({ title, initial, loadError, onSave, onClose }: SkillMdDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[760px]">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <ErrorAlert error={loadError} />
        {initial === undefined ? (
          !loadError && <p className="text-[14px] text-ink-500">{t.common.loading}</p>
        ) : (
          <SkillMdForm initial={initial} onSave={onSave} onClose={onClose} />
        )}
      </DialogContent>
    </Dialog>
  )
}

export function SkillMdForm({ initial, onSave, onClose, saveLabel = t.common.save }: { initial: string; onSave: (content: string) => Promise<unknown>; onClose?: () => void; saveLabel?: string }) {
  const [content, setContent] = useState(initial)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const save = async () => {
    setPending(true)
    setError(null)
    try {
      await onSave(content)
      onClose?.()
    } catch (e) {
      setError(e)
    } finally {
      setPending(false)
    }
  }
  return (
    <div className="grid gap-4">
      <Textarea aria-label="SKILL.md" className="min-h-80 px-4 py-3.5 font-mono text-[13px] leading-[1.7]" value={content} onChange={(e) => setContent(e.target.value)} />
      <ErrorAlert error={error} />
      <DialogFooter>
        {onClose && (
          <Button variant="secondary" onClick={onClose}>
            {t.common.cancel}
          </Button>
        )}
        <Button disabled={pending || content.trim() === ''} onClick={() => void save()}>
          {saveLabel}
        </Button>
      </DialogFooter>
    </div>
  )
}
