import type { components } from '@/api/schema.gen'
import { formatDayClock } from './format'

type S = components['schemas']

export const tasks = {
  title: '任务', back: '返回项目任务', chooseProject: '选择项目查看任务', intro: '本项目的任务，由项目 Manager 创建',
  empty: '还没有任务。可以在对话中请项目 Manager 创建任务。', manager: '前往 Manager 对话', settings: '项目设置',
  number: '编号', numberLabel: (number: number) => `#${number}`,
  // Flow ruler in the stub on the right of the task detail (redesign step 5).
  ruler: {
    todo: '还没到', here: '在看', done: '已全部走完', notStarted: '还没开始',
    visit: (visit: number, attempt: number) => (attempt > 1 ? `第 ${visit} 次 · 第 ${attempt} 次尝试` : `第 ${visit} 次`),
    jump: (label: string, to: string) => `${label}到 ${to}`,
    used: (used: number, max: number) => `已用 ${used}/${max} 次`,
    at: (step: number) => `走到第 ${step} 步`,
  },
  // Task detail page (redesign step 5).
  page: {
    meta: (created: string, updated: string) => `建于 ${formatDayClock(created)} · 更新 ${formatDayClock(updated)}`,
    sendTo: '发给', seeMaterials: '看节点资料', backToConversation: '回到这次的对话', inputs: '输入', artifacts: '产物', expand: '展开', collapse: '收起',
    visits: (count: number) => `${count} 次访问`,
    artifactCount: (count: number) => `${count} 份产物`,
    version: (visit: number, attempt: number) => `V${visit}·A${attempt}`,
    ran: (start: string, end: string | null) => (end ? `${formatDayClock(start)} 开始 · ${formatDayClock(end)} 停下` : `${formatDayClock(start)} 开始`),
    versionLink: (step: string, title: string, visit: number) => `${step} ${title} · 第 ${visit} 次的输出`,
  },
  task: '任务', state: '状态', currentNode: '当前节点', responsible: '责任 Agent', none: '—', more: '加载更多',
  wrongProject: '这个任务不属于当前项目。', missing: '找不到这个任务。', workflow: '固定流程',
  targetChanged: '发送目标与当前能力不一致，草稿保留，请核对原节点。', receiptMismatch: '发送回执目标不一致，草稿保留，请核对原执行记录。', sendUnknown: '上次发送结果未知，草稿保留。重试只核对原消息，不自动改投或发送新草稿。', sendAccepted: '上次消息已接收并进入队列。', sending: '正在发送…', retryOriginalMessage: '重试确认原消息', targetDetails: '目标详情', instance: '任务实例', conversationTab: '对话', materials: '节点资料', execution: '执行分界', incomplete: '本次执行还有消息未加载。', newMessages: '有新消息', details: '展开详情', draft: '消息草稿', draftPlaceholder: (name: string) => `给 ${name} 发消息`, draftTarget: '任务内发送目标', draftChanged: '原执行状态已变化，草稿保留在原节点；另发请手动复制。', copyDraft: '复制草稿', nodeMessagesUnsupported: '当前服务尚未启用节点消息',
  record: '节点记录', selectRecord: '访问与重试记录', selectNode: '选择一个节点查看记录', noRecord: '这个节点尚未被访问。', invalidRecord: '找不到指定记录，请重新选择节点。',
  visit: (visit: number, attempt: number) => `第 ${visit} 次访问 · 第 ${attempt} 次尝试`,
  statuses: { preparing: '准备中', running: '执行中', waiting: '等待回复', blocked: '阻塞', completed: '已完成', cancelled: '已取消' } satisfies Record<S['TaskStatus'], string>,
  nodeStatuses: { queued: '等待执行', running: '执行中', submitted: '结果已提交，等待当前轮结束', waiting: '等待回复', blocked: '阻塞', completed: '已完成', cancelled: '已取消' } satisfies Record<S['TaskNodeStatus'], string>,
  cleanup: { none: '尚未归档', pending: '正在归档与回收', archived: '记录已归档，副本已回收', failed: '归档或回收失败，记录保留' } satisfies Record<S['TaskDetail']['cleanup'], string>,
  history: '正在查看历史记录', currentChanged: '查看当前节点',
  conversation: '节点会话', noMessages: '还没有会话消息。', readonly: '只读的任务节点会话', input: '实际输入', output: '输出', noOutput: '尚未提交输出',
  inspectOnly: '执行异常，已提交结果仅供检查，不作为成功结果交给下游。', versions: '输入来源记录', source: (name: string) => `输入 ${name} 的来源`,
  reason: '等待或失败原因', artifacts: '已保存成果', noArtifacts: '没有显式保存的成果。', download: '下载',
  retry: '重试当前节点', retryNote: '重试说明（可选）', retryBody: '沿用本任务的副本、会话和启动配置。关闭上次未回答的问题并保留记录，由 Agent 重新判断是否需要提问。',
  cancel: '取消任务', cancelTitle: '取消这个任务？', cancelBody: '停止任务执行，关闭未结束的问题，保留运行记录、会话归档和已保存成果，并回收任务副本。',
  question: '待回答问题', questionState: { open: '待回答', answered: '已回答', expired: '已到期', closed: '已关闭', cancelled: '已取消' }, answer: '前往回答', cannotAnswer: '当前身份不能回答这个问题。',
  targetQuestion: '正在回复指定原问题', targetUnavailable: '指定原问题无法确认可答，请返回任务检查最新状态。',
  targetTerminal: (state: string) => `原问题${state}，不能继续回答。`,
  // Status filter along the top edge of the task list sheet (filters only the tasks loaded so far) and
  // the progress in the stub. “进行中” (In progress) covers preparing, running and waiting for reply.
  updated: '更新', updatedAt: (time: string) => formatDayClock(time),
  filters: { label: '状态筛选', all: '全部', active: '进行中', blocked: '阻塞', completed: '已完成' },
  progress: { title: '进度', active: '进行中', blocked: '阻塞', completed: '已完成' },
  loading: '加载任务中…', updating: '实时更新中', reconnecting: '连接中断，正在重连…',
  // Task-turn error codes that are not run error codes (those come from chat.runError): set by the task coordinator.
  turnErrors: {
    task_conflict: '节点或执行条件已变化，这条消息没有执行',
    server_restarted: '服务重启，这一轮没有重放',
  } as Record<string, string>,
  messageError: '执行错误', user: '平台', assistant: 'Agent', messageStatuses: { queued: '排队中', starting: '启动中', running: '回复中', aborting: '正在打断', completed: '已完成', failed: '失败', interrupted: '已打断' } satisfies Record<S['TaskMessage']['status'], string>,
} as const
