// 控制台的运行参数。都可以在构建时通过 VITE_ 环境变量覆盖。
function numberFromEnv(value: string | undefined, fallback: number): number {
  const parsed = Number(value)
  return value !== undefined && value !== '' && Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}

export const config = {
  apiBase: (import.meta.env.VITE_LOOP_NOW_API_BASE || 'http://127.0.0.1:8780/api/v1').replace(/\/+$/, ''),
  agentListPollMs: numberFromEnv(import.meta.env.VITE_LOOP_NOW_AGENT_LIST_POLL_MS, 5000),
  eventRetryMinMs: numberFromEnv(import.meta.env.VITE_LOOP_NOW_EVENT_RETRY_MIN_MS, 1000),
  eventRetryMaxMs: numberFromEnv(import.meta.env.VITE_LOOP_NOW_EVENT_RETRY_MAX_MS, 30000),
  uploadConcurrency: numberFromEnv(import.meta.env.VITE_LOOP_NOW_UPLOAD_CONCURRENCY, 4),
  messagePageSize: 50,
  // 概览的用量与异常列表的刷新间隔；窗口重新获得焦点时也刷新。
  overviewPollMs: numberFromEnv(import.meta.env.VITE_LOOP_NOW_OVERVIEW_POLL_MS, 30000),
  // 概览的默认值（用户已确认）：用量看今天、按 Agent；异常看最近 24 小时。
  overviewDefaults: {
    usageRange: 'today' as const,
    usageDimension: 'agent' as const,
    anomalyWindowHours: 24,
  },
  anomalyPageSize: 20,
  // 收件箱：线索列表、Agent 与外部身份收件箱每页的条数。
  inboxPageSize: 20,
  // 签发令牌时预选的有效期（天，用户已确认）。
  tokenDefaultDays: 30,
  // 运行记录默认收起（用户已确认）。
  runRecordExpanded: false,
  // 回复进行中展开运行记录时，同一执行两次重新获取之间至少间隔这么久。
  timelineRefreshMinMs: 1000,
  // 排队消息无法转为打断时，输入框上方的轻提示显示这么久后自动消失。
  queueNoticeMs: 6000,
  // 文件上传全部成功后，进度卡片停留多久再收起。
  uploadPanelMs: 4000,
}
