// The console's runtime parameters. All of them can be overridden at build time with VITE_ environment variables.
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
  // Refresh interval of the overview's usage and anomaly lists; they also refresh when the window regains focus.
  overviewPollMs: numberFromEnv(import.meta.env.VITE_LOOP_NOW_OVERVIEW_POLL_MS, 30000),
  // Overview defaults (confirmed by the user): usage shows today, by Agent; anomalies show the last 24 hours.
  overviewDefaults: {
    usageRange: 'today' as const,
    usageDimension: 'agent' as const,
    anomalyWindowHours: 24,
  },
  anomalyPageSize: 20,
  // Inbox: items per page in the thread list and in the inboxes of Agents and external identities.
  inboxPageSize: 20,
  // The validity period preselected when issuing a token (days, confirmed by the user).
  tokenDefaultDays: 30,
  // The run log is collapsed by default (confirmed by the user).
  runRecordExpanded: false,
  // When the run log is expanded while a reply is in progress, at least this long passes between two refetches of the same run.
  timelineRefreshMinMs: 1000,
  // When a queued message cannot be turned into an interrupt, the toast above the input is shown this long and then disappears.
  queueNoticeMs: 6000,
  // How long the progress card stays after all file uploads succeed, before it collapses.
  uploadPanelMs: 4000,
}

// The API URL for display. When the server serves the console on the same port, apiBase is the relative path /api/v1 and is resolved to
// an absolute URL against the current page; returned as is when it cannot be resolved.
export function absoluteApiBase(): string {
  try {
    return new URL(config.apiBase, window.location.href).href.replace(/\/+$/, '')
  } catch {
    return config.apiBase
  }
}
