/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_LOOP_NOW_API_BASE?: string
  readonly VITE_LOOP_NOW_AGENT_LIST_POLL_MS?: string
  readonly VITE_LOOP_NOW_EVENT_RETRY_MIN_MS?: string
  readonly VITE_LOOP_NOW_EVENT_RETRY_MAX_MS?: string
  readonly VITE_LOOP_NOW_UPLOAD_CONCURRENCY?: string
  readonly VITE_LOOP_NOW_OVERVIEW_POLL_MS?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
