import { useSyncExternalStore } from 'react'
import { Link, Navigate, useNavigate } from 'react-router'
import { useProjects } from '@/api/queries'
import { tasksPath } from '@/api/tasks'
import { lastScope, resolveLastScope } from '@/lib/scope'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { t } from '@/i18n'

export function TasksEntry() {
  const navigate = useNavigate()
  const projects = useProjects()
  const remembered = useSyncExternalStore(lastScope.subscribe, lastScope.get)
  const scope = projects.data ? resolveLastScope(projects.data.projects, remembered) : null
  if (!scope) return <div className="p-6"><ErrorAlert error={projects.error} />{t.common.loading}</div>
  if (scope.kind === 'project') return <Navigate replace to={tasksPath(scope.project.name)} />
  return <Dialog open onOpenChange={(open) => { if (!open) void navigate('/platform/agents') }}><DialogContent showCloseButton={false}><DialogHeader><DialogTitle>{t.tasks.chooseProject}</DialogTitle></DialogHeader>
    <div className="grid gap-3">{projects.data?.projects.map((p) => <Button key={p.id} asChild variant="secondary"><Link to={tasksPath(p.name)}>{p.name}</Link></Button>)}
    <Button asChild variant="secondary"><Link to="/platform/agents">{t.common.cancel}</Link></Button></div>
  </DialogContent></Dialog>
}
