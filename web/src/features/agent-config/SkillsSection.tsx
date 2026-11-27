import { useState } from 'react'
import { PlusIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SkillMdDialog } from '@/components/SkillMdDialog'
import { ApiError, type Schemas } from '@/api/client'
import { useAddAgentSkill, useAgentSkill, useAgentSkills, useRecopyAgentSkill, useRemoveAgentSkill, useSkills, useUpdateAgentSkillMarkdown } from '@/api/queries'
import { t } from '@/i18n'
import { Section } from './Section'

const listClass =
  'grid min-w-0 divide-y divide-hairline overflow-hidden rounded-card border border-hairline bg-surface-control'

export function SkillsSection({ agentId }: { agentId: string }) {
  const skills = useAgentSkills(agentId)
  const library = useSkills()
  const [adding, setAdding] = useState(false)
  const templateName = (id: string | null) => (id ? library.data?.skills.find((s) => s.id === id)?.name : undefined)
  const items = skills.data?.skills ?? []

  return (
    <Section
      id="skills"
      title={t.config.skills.title}
      count={skills.data?.skills.length}
      actions={
        <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
          <PlusIcon />
          {t.config.skills.add}
        </Button>
      }
    >
      {skills.isError && <ErrorAlert error={skills.error} />}
      {skills.data?.skills.length === 0 && <p className="text-[13px] text-ink-500">{t.config.skills.empty}</p>}
      {items.length > 0 && (
        <ul className={listClass}>
          {items.map((skill) => (
            <SkillItem key={skill.id} agentId={agentId} skill={skill} sourceName={templateName(skill.sourceTemplateId)} />
          ))}
        </ul>
      )}
      {adding && <AddSkillDialog agentId={agentId} onClose={() => setAdding(false)} />}
    </Section>
  )
}

function SkillItem({ agentId, skill, sourceName }: { agentId: string; skill: Schemas['AgentSkillSummary']; sourceName: string | undefined }) {
  const [editing, setEditing] = useState(false)
  const [recopying, setRecopying] = useState(false)
  const [removing, setRemoving] = useState(false)
  const recopy = useRecopyAgentSkill(agentId)
  const remove = useRemoveAgentSkill(agentId)
  const sourceDeleted = skill.sourceTemplateId === null

  return (
    <li className="flex min-h-12 flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-2.5" aria-label={skill.name}>
      <div className="flex min-w-0 flex-[1_1_240px] flex-col gap-0.5">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <span className="font-mono text-[13.5px] font-semibold">{skill.name}</span>
          {sourceDeleted ? (
            <Badge tone="neutral" className="bg-tint-yellow">
              {t.config.skills.sourceDeleted}
            </Badge>
          ) : (
            <span className="text-[12px] text-ink-500">{t.config.skills.source(sourceName ?? skill.name)}</span>
          )}
        </div>
        <p className="truncate text-[12.5px] text-ink-700" title={skill.description}>
          {skill.description}
        </p>
        {sourceDeleted && <p className="text-[12px] text-ink-500">{t.config.skills.recopyDisabled}</p>}
      </div>
      <div className="ml-auto flex shrink-0 flex-wrap items-center gap-1">
        <Button size="xs" variant="secondary" onClick={() => setEditing(true)}>
          {t.config.skills.editSkillMd}
        </Button>
        <Button size="xs" variant="ghost" disabled={sourceDeleted} title={sourceDeleted ? t.config.skills.recopyDisabled : undefined} onClick={() => setRecopying(true)}>
          {t.config.skills.recopy}
        </Button>
        <Button size="xs" variant="ghost" onClick={() => setRemoving(true)}>
          {t.common.remove}
        </Button>
      </div>
      {editing && <AgentSkillEditor agentId={agentId} skill={skill} onClose={() => setEditing(false)} />}
      <ConfirmDialog
        open={recopying}
        onOpenChange={setRecopying}
        title={t.config.skills.recopyTitle}
        body={t.config.skills.recopyBody}
        confirmLabel={t.config.skills.recopy}
        destructive
        onConfirm={() => recopy.mutateAsync(skill.id)}
      />
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t.config.skills.removeTitle(skill.name)}
        body={t.config.skills.removeBody}
        confirmLabel={t.common.remove}
        destructive
        onConfirm={() => remove.mutateAsync(skill.id)}
      />
    </li>
  )
}

function AddSkillDialog({ agentId, onClose }: { agentId: string; onClose: () => void }) {
  const library = useSkills()
  const add = useAddAgentSkill(agentId)
  const duplicate = add.error instanceof ApiError && add.error.code === 'name_conflict'
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !add.isPending) onClose() }}>
      <DialogContent kind="create" className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{t.config.skills.addTitle}</DialogTitle>
        </DialogHeader>
        {library.isPending && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
        {library.isError && <ErrorAlert error={library.error} />}
        {library.data?.skills.length === 0 && <p className="text-[13.5px] text-ink-500">{t.config.skills.libraryEmpty}</p>}
        <ul className="grid max-h-80 divide-y divide-hairline overflow-y-auto rounded-card border border-hairline bg-surface-control">
          {library.data?.skills.map((template) => (
            <li key={template.id} className="flex items-center gap-3 px-3.5 py-2.5" aria-label={template.name}>
              <div className="flex min-w-0 flex-1 flex-col">
                <span className="font-mono text-[13.5px] font-semibold">{template.name}</span>
                <span className="text-[12.5px] text-ink-700">{template.description}</span>
              </div>
              <Button size="xs" disabled={add.isPending} onClick={() => void add.mutateAsync(template.id).then(onClose, () => {})}>
                {t.common.add}
              </Button>
            </li>
          ))}
        </ul>
        {add.error && <ErrorAlert error={add.error} />}
        {duplicate && <p className="text-[13.5px] text-ink-700">{t.config.skills.duplicateHint}</p>}
      </DialogContent>
    </Dialog>
  )
}

function AgentSkillEditor({ agentId, skill, onClose }: { agentId: string; skill: Schemas['AgentSkillSummary']; onClose: () => void }) {
  const detail = useAgentSkill(agentId, skill.id)
  const update = useUpdateAgentSkillMarkdown(agentId)
  return (
    <SkillMdDialog
      title={t.config.skills.editTitle(skill.name)}
      initial={detail.data?.skillMd}
      loadError={detail.error}
      onSave={(content) => update.mutateAsync({ agentSkillId: skill.id, content })}
      onClose={onClose}
    />
  )
}
