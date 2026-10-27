import { useId, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useCreateProject } from '@/api/queries'
import { t } from '@/i18n'
import { isAgentNameConflict, projectNameError, unmatchedFieldError } from '@/lib/errors'
import { nameRuleError } from '@/lib/names'
import { agentsPath } from '@/lib/scope'

// 新建项目：只有名称一项。成功后进入新项目的 Agent 页。
export function CreateProjectDialog({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate()
  const create = useCreateProject()
  const [name, setName] = useState('')
  const [localError, setLocalError] = useState<string | undefined>()
  const hintId = useId()

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const rule = name.trim() ? nameRuleError(name) : t.projects.create.nameRequired
    setLocalError(rule)
    if (rule) return
    const project = await create.mutateAsync({ name }).catch(() => null)
    if (project) {
      onClose()
      void navigate(agentsPath({ kind: 'project', project }))
    }
  }

  const nameError = localError ?? projectNameError(create.error)
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[480px]">
        <DialogHeader>
          <DialogTitle>{t.projects.create.title}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-[18px]" onSubmit={(e) => void submit(e)} noValidate>
          <div className="grid gap-1.5">
            <Label htmlFor="create-project-name">{t.projects.create.name}</Label>
            <Input
              id="create-project-name"
              value={name}
              readOnly={create.isPending}
              aria-invalid={nameError ? true : undefined}
              aria-describedby={hintId}
              onChange={(e) => setName(e.target.value)}
            />
            {nameError && (
              <p role="alert" className="text-[12.5px] leading-snug font-bold text-red-text">
                {nameError}
              </p>
            )}
            <p id={hintId} className="text-[12.5px] leading-snug text-ink-500">
              {t.projects.create.hint}
            </p>
          </div>
          {!isAgentNameConflict(create.error) && <ErrorAlert error={unmatchedFieldError(create.error, (field) => field === 'name')} />}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              {t.common.cancel}
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? t.projects.create.submitting : t.projects.create.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
