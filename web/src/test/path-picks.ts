import { http, HttpResponse } from 'msw'
import { onTestFinished } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context, errorBody } from './api'
import { server } from './server'

// What the server's dialog answers: a path, null (cancelled), or a refusal.
export type PickAnswer = { path: string | null } | { status: number; code: Schemas['ErrorCode']; message?: string }

const respond = (answer: PickAnswer) =>
  'path' in answer ? HttpResponse.json({ path: answer.path } satisfies Schemas['PathPick']) : HttpResponse.json(errorBody(answer.code, answer.message), { status: answer.status })

// The console in a browser on a computer whose server can show its own dialog, for one test: every /context handler answers pathPicker
// true (they all send the shared `context`), and POST /path-picks answers with `state.answer`. 'hold' keeps the dialog open until
// state.release(answer). Requests and aborted requests are counted.
export function serverShowsDialog(answer: PickAnswer | 'hold' = { path: null }) {
  context.pathPicker = true
  onTestFinished(() => {
    context.pathPicker = false
  })
  const state = {
    answer,
    requests: [] as Schemas['PathPickRequest'][],
    aborted: 0,
    release: (() => {
      throw new Error('no dialog is open')
    }) as (answer: PickAnswer) => void,
  }
  server.use(
    http.post(apiUrl('/path-picks'), async ({ request }) => {
      request.signal.addEventListener('abort', () => (state.aborted += 1))
      state.requests.push((await request.json()) as Schemas['PathPickRequest'])
      const answer = state.answer === 'hold' ? await new Promise<PickAnswer>((resolve) => (state.release = resolve)) : state.answer
      return respond(answer)
    }),
  )
  return state
}
