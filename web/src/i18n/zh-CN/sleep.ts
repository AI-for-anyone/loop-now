import type { components } from '@/api/schema.gen'
import { formatDayClock, formatSlept } from './format'

type S = components['schemas']

export const sleep = {
  title: 'sleep 等待',
  until: (at: string) => `sleep 至 ${formatDayClock(at)}`,
  remaining: (seconds: number) => `剩余 ${Math.floor(seconds / 60)} 分 ${seconds % 60} 秒`,
  awaitingWake: '等待服务端安排唤醒',
  instances: '任务内普通对话 sleep',
  instance: (id: string) => `运行实例 ${id}`,
  wakeReference: (id: string) => `唤醒关联 ${id}`,
  // 对话里的唤醒行：到期唤醒一行淡色元信息；被新消息提前唤醒时只在那条消息旁标一下。sleep ID 只在悬停提示里。
  woke: 'sleep 到期唤醒',
  wokeEarly: '提前唤醒 sleep',
  slept: (ms: number) => `睡了 ${formatSlept(ms)}`,
  idTip: (id: string) => `sleep ${id}`,
  states: { requested: '已登记 sleep，当前轮尚未结束', sleeping: '进入 sleep 等待', woken: 'sleep 已结束，继续工作已安排', cancelled: 'sleep 已失效' } satisfies Record<S['SleepRecord']['state'], string>,
  endReasons: { deadline: '到期唤醒', queued_work: '由新工作提前唤醒', execution_failed: '本轮执行失败', interrupted: '本轮被打断', task_ended: '任务已结束', node_changed: '节点已变化', session_reopened: '会话已重开', owner_deleted: '所属对象已删除' } satisfies Record<NonNullable<S['SleepRecord']['endReason']>, string>,
} as const
