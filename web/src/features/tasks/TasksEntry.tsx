import { useSyncExternalStore } from 'react'
import { Link, Navigate, useNavigate } from 'react-router'
import { useProjects } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { t } from '@/i18n'
import { lastScope, resolveLastScope } from '@/lib/scope'

export function TasksEntry() {
  const navigate = useNavigate()
  const projects = useProjects()
  const remembered = useSyncExternalStore(lastScope.subscribe, lastScope.get)
  const scope = projects.data ? resolveLastScope(projects.data.projects, remembered) : null

  if (scope?.kind === 'project') return <Navigate replace to={tasksPath(scope.project.name)} />

  return (
    <PageFrame>
      <PageHeader crumbs={[]} title={t.tasks.title} />
      <Sheet className="flex-1">
        <PageBody width="narrow">
          <ErrorAlert error={projects.error} />
          {!scope && !projects.isError && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
        </PageBody>
      </Sheet>
      {scope && (
        <Dialog open onOpenChange={(open) => { if (!open) void navigate('/platform/agents') }}>
          <DialogContent kind="choose" showCloseButton={false}>
            <DialogHeader>
              <DialogTitle>{t.tasks.chooseProject}</DialogTitle>
            </DialogHeader>
            <div className="grid gap-2.5">
              {projects.data?.projects.map((p) => (
                <Button key={p.id} asChild variant="secondary">
                  <Link to={tasksPath(p.name)}>{p.name}</Link>
                </Button>
              ))}
              <Button asChild variant="ghost">
                <Link to="/platform/agents">{t.common.cancel}</Link>
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      )}
    </PageFrame>
  )
}
