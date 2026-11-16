// Isolated component demo only. No API/provider requests or product route.
import { useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router'
import type { Schemas } from '@/api/client'
import { LarkUserLogin } from '@/features/project-credentials/LarkUserLogin'
import { LarkAccountDetails } from '@/features/project-credentials/LarkAccountDetails'
import { TaskLarkAccountSwitch, type TaskLarkAccountSwitchProps } from '@/features/tasks/TaskLarkAccountSwitch'
import { larkLoginLocatorKey, taskLarkSwitchLocatorKey, useOriginalActionLocator } from '@/lib/useOriginalActionLocator'
import { makeTaskDetail } from './tasks'
import '../index.css'

const projectId = '00000000-0000-7000-8000-000000000001'
const app: Schemas['ProjectCredentialAccount'] = {
  id: '10000000-0000-7000-8000-000000000001', projectId, purpose: 'cli', cli: 'lark-cli', provider: null, githubIdentity: null,
  name: 'QA app · synthetic data', accountLabel: null, revision: 1, currentVersionId: '20000000-0000-7000-8000-000000000001',
  authMethod: null, status: 'saved', template: null, createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-20T00:00:00Z', deletedAt: null,
}
const user: Schemas['ProjectCredentialAccount'] = { ...app, id: '10000000-0000-7000-8000-000000000002', purpose: 'cli', cli: 'lark-cli', name: 'QA user account · synthetic data', authMethod: 'oauth', status: 'logged_in', currentVersionId: '20000000-0000-7000-8000-000000000002', larkIdentity: { brand: 'feishu', appId: 'synthetic-app', openId: 'synthetic-user', displayName: 'Synthetic verified user', grantedScopes: null, verifiedAt: '2026-10-20T00:00:00Z' } }
const task = makeTaskDetail(projectId), node = task.nodes[1]
const members: TaskLarkAccountSwitchProps['members'] = [
  { identity: node, title: 'Deploy-for-QA publisher', originalPermission: 'Original snapshot allows Lark (synthetic)', oldBinding: 'App V1 (synthetic)' },
  { identity: { agentId: '00000000-0000-7000-8000-000000000200', runtimeId: '00000000-0000-7000-8000-000000000201', agentName: 'QA result follow-up' }, title: 'Result follow-up', originalPermission: 'Original snapshot allows Lark (synthetic)', oldBinding: 'Originally captured app V3 (synthetic)' },
]
export function Fixture() {
  const login = useOriginalActionLocator(larkLoginLocatorKey(projectId)), action = useOriginalActionLocator(taskLarkSwitchLocatorKey(task.task.id))
  const [op, setOp] = useState<{ id: string; phase: Schemas['CLIAuthOperation']['status'] } | null>(null)
  const [recovery, setRecovery] = useState<'unknown' | 'not-found' | undefined>()
  const [scenario, setScenario] = useState('ready'), [sourceRevision, setSourceRevision] = useState(1)
  const [stage, setStage] = useState<TaskLarkAccountSwitchProps['stage']>('review')
  const [calls, setCalls] = useState({ login: 0, loginReads: 0, switches: 0, switchReads: 0, opens: 0 })
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const phase = op?.id === login.id ? op.phase : undefined
  const saved = phase === 'succeeded'
  return <main className="mx-auto grid max-w-5xl min-w-0 gap-5 p-4 text-ink">
    <h1 className="text-2xl font-bold">Lark: the real React components with injected callbacks</h1>
    <p className="rounded-card bg-tint-yellow p-3">All data is synthetic. No HTTP API, real authorization, database or task operations; this is neither the product wiring nor an App acceptance check.</p>
    <label>Scenario <select aria-label="Injected scenario" value={scenario} onChange={e => setScenario(e.target.value)}><option value="ready">Actionable</option><option value="unsupported">Older backend without support</option><option value="busy">Affected member busy</option><option value="no-sources">No source app</option></select></label>
    <div className="flex flex-wrap gap-3"><button onClick={() => setSourceRevision(v => v + 1)}>Simulate a source revision change</button><button disabled={!login.id} onClick={() => setRecovery('not-found')}>Original action not found</button><button disabled={!login.id} onClick={() => setRecovery('unknown')}>Original action result unknown</button><button disabled={!op} onClick={() => { if (op) setOp({ ...op, phase: 'failed' }) }}>This authorization failed</button></div>
    <LarkAccountDetails account={app} />
    <LarkUserLogin projectId={projectId} available={scenario !== 'unsupported'} sources={scenario === 'no-sources' ? [] : [{ ...app, revision: sourceRevision }]} operationId={login.id} phase={phase} recovery={recovery ?? (login.id && !op ? 'unknown' : undefined)} operationSource={app.name}
      authorizationReady expiresAt="2030-01-01T00:00:00Z" userCode={phase === 'awaiting_authorization' ? 'Synthetic user code, not real' : null}
      applicationSetup={<a href="#application-setup">Original app setup entry (illustration only)</a>}
      savedAccount={saved ? <LarkAccountDetails account={user} identity="Synthetic verified user" grantedScopes={null} /> : undefined}
      onStart={async () => { login.begin(async id => { setCalls(c => ({ ...c, login: c.login + 1 })); setRecovery(undefined); setOp({ id, phase: 'pending' }); timer.current = setTimeout(() => setOp({ id, phase: 'awaiting_authorization' }), 500) }, !!phase && phase !== 'pending' && phase !== 'awaiting_authorization') }}
      onRecover={async () => { setCalls(c => ({ ...c, loginReads: c.loginReads + 1 })); setRecovery(undefined) }}
      onCancel={async id => setOp({ id, phase: 'cancelled' })}
      onOpenAuthorization={() => { setCalls(c => ({ ...c, opens: c.opens + 1 })); if (login.id) setOp({ id: login.id, phase: 'succeeded' }) }} />
    <h2 className="text-xl font-bold">A task with two fixed members · separate actions</h2>
    <TaskLarkAccountSwitch task={task.task} node={node} members={members} accounts={saved ? [user] : []} reviewKey="synthetic-fixed-members" eligible={scenario !== 'busy' && scenario !== 'unsupported'} disabledReason={scenario === 'busy' ? 'Follow-up member busy: whole group refused (synthetic)' : 'Component capability not enabled yet (synthetic)'} stage={action.id && stage === 'review' ? 'unknown' : stage} actionId={action.id} dispatchStatus={stage === 'committed' ? 'failed' : undefined}
      onConfirm={async () => { action.begin(async () => { setCalls(c => ({ ...c, switches: c.switches + 1 })); setStage('committed') }) }}
      onRecover={async () => setCalls(c => ({ ...c, switchReads: c.switchReads + 1 }))} />
    <button onClick={() => setStage('unknown')}>Simulate an unknown recovery action</button>
    <output data-testid="injected-calls" className="break-all">{JSON.stringify(calls)}</output>
  </main>
}
createRoot(document.getElementById('root')!).render(<BrowserRouter><Fixture /></BrowserRouter>)
