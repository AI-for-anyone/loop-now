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
  name: 'QA应用 · 合成数据', accountLabel: null, revision: 1, currentVersionId: '20000000-0000-7000-8000-000000000001',
  authMethod: null, status: 'saved', template: null, createdAt: '2026-10-20T00:00:00Z', updatedAt: '2026-10-20T00:00:00Z', deletedAt: null,
}
const user: Schemas['ProjectCredentialAccount'] = { ...app, id: '10000000-0000-7000-8000-000000000002', purpose: 'cli', cli: 'lark-cli', name: 'QA用户账号 · 合成数据', authMethod: 'oauth', status: 'logged_in', currentVersionId: '20000000-0000-7000-8000-000000000002', larkIdentity: { brand: 'feishu', appId: 'synthetic-app', openId: 'synthetic-user', displayName: '合成核实用户', grantedScopes: null, verifiedAt: '2026-10-20T00:00:00Z' } }
const task = makeTaskDetail(projectId), node = task.nodes[1]
const members: TaskLarkAccountSwitchProps['members'] = [
  { identity: node, title: '部署提测 publisher', originalPermission: '原snapshot允许Lark（合成）', oldBinding: '应用V1（合成）' },
  { identity: { agentId: '00000000-0000-7000-8000-000000000200', runtimeId: '00000000-0000-7000-8000-000000000201', agentName: 'QA结果跟进员' }, title: '结果跟进 followup', originalPermission: '原snapshot允许Lark（合成）', oldBinding: '原捕获应用V3（合成）' },
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
    <h1 className="text-2xl font-bold">Lark 实际React组件 · 回调注入示例</h1>
    <p className="rounded-card bg-tint-yellow p-3">全部为合成数据。没有HTTP API、真实授权、数据库或任务操作；不代表产品接线或App验收。</p>
    <label>场景 <select aria-label="注入场景" value={scenario} onChange={e => setScenario(e.target.value)}><option value="ready">可操作</option><option value="unsupported">旧后台不支持</option><option value="busy">受影响成员忙碌</option><option value="no-sources">无来源应用</option></select></label>
    <div className="flex flex-wrap gap-3"><button onClick={() => setSourceRevision(v => v + 1)}>模拟来源修订变化</button><button disabled={!login.id} onClick={() => setRecovery('not-found')}>原操作未找到</button><button disabled={!login.id} onClick={() => setRecovery('unknown')}>原操作结果未知</button><button disabled={!op} onClick={() => { if (op) setOp({ ...op, phase: 'failed' }) }}>本次授权失败</button></div>
    <LarkAccountDetails account={app} />
    <LarkUserLogin projectId={projectId} available={scenario !== 'unsupported'} sources={scenario === 'no-sources' ? [] : [{ ...app, revision: sourceRevision }]} operationId={login.id} phase={phase} recovery={recovery ?? (login.id && !op ? 'unknown' : undefined)} operationSource={app.name}
      authorizationReady expiresAt="2030-01-01T00:00:00Z" userCode={phase === 'awaiting_authorization' ? '合成用户码·非真实' : null}
      applicationSetup={<a href="#application-setup">原应用配置入口（仅示意）</a>}
      savedAccount={saved ? <LarkAccountDetails account={user} identity="合成核实用户" grantedScopes={null} /> : undefined}
      onStart={async () => { login.begin(async id => { setCalls(c => ({ ...c, login: c.login + 1 })); setRecovery(undefined); setOp({ id, phase: 'pending' }); timer.current = setTimeout(() => setOp({ id, phase: 'awaiting_authorization' }), 500) }, !!phase && phase !== 'pending' && phase !== 'awaiting_authorization') }}
      onRecover={async () => { setCalls(c => ({ ...c, loginReads: c.loginReads + 1 })); setRecovery(undefined) }}
      onCancel={async id => setOp({ id, phase: 'cancelled' })}
      onOpenAuthorization={() => { setCalls(c => ({ ...c, opens: c.opens + 1 })); if (login.id) setOp({ id: login.id, phase: 'succeeded' }) }} />
    <h2 className="text-xl font-bold">任务固定两成员 · 独立动作</h2>
    <TaskLarkAccountSwitch task={task.task} node={node} members={members} accounts={saved ? [user] : []} reviewKey="synthetic-fixed-members" eligible={scenario !== 'busy' && scenario !== 'unsupported'} disabledReason={scenario === 'busy' ? '结果跟进成员忙碌：整组拒绝（合成）' : '组件能力尚未启用（合成）'} stage={action.id && stage === 'review' ? 'unknown' : stage} actionId={action.id} dispatchStatus={stage === 'committed' ? 'failed' : undefined}
      onConfirm={async () => { action.begin(async () => { setCalls(c => ({ ...c, switches: c.switches + 1 })); setStage('committed') }) }}
      onRecover={async () => setCalls(c => ({ ...c, switchReads: c.switchReads + 1 }))} />
    <button onClick={() => setStage('unknown')}>模拟恢复动作未知</button>
    <output data-testid="injected-calls" className="break-all">{JSON.stringify(calls)}</output>
  </main>
}
createRoot(document.getElementById('root')!).render(<BrowserRouter><Fixture /></BrowserRouter>)
