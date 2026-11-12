import assert from 'node:assert/strict'
import { it } from 'node:test'

import type { PluginEvent, RunOrigin } from '../src/events.ts'

it('RunOrigin 接受 api/openapi.yaml 的 console_compact', () => {
  const origin: RunOrigin = 'console_compact'
  const event: PluginEvent<'run.before_start'> = {
    id: '01TEST',
    type: 'run.before_start',
    at: '2026-01-01T00:00:00Z',
    projectId: null,
    agentId: null,
    taskId: null,
    runId: null,
    origin,
    messageId: null,
  }
  assert.equal(event.origin, 'console_compact')
  assert.equal(event.messageId, null)
})
