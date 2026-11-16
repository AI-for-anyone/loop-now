import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { t } from '@/i18n'
import { ErrorAlert } from './ErrorAlert'

interface SkillMdDialogProps {
  title: string
  // undefined while still loading.
  initial: string | undefined
  loadError?: unknown
  onSave: (content: string) => Promise<unknown>
  onClose: () => void
}

// Edit SKILL.md in place (the Agent's copy of the Skill): edits are kept when saving fails. The editor is monospace on white, with the
// buttons at the bottom right.
export function SkillMdDialog({ title, initial, loadError, onSave, onClose }: SkillMdDialogProps) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent kind="edit" className="sm:max-w-[760px]">
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
