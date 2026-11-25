import { delay, http, HttpResponse } from 'msw'
import type { Schemas } from '@/api/client'
import { apiUrl } from './api'

type Writer = Schemas['TextGenerationWriter']
type Settings = Schemas['TextGenerationSettings']

// Who writes by default in tests: Automatic, which is Claude Code with its Haiku model (the models of test/runtimes' catalogs).
export const haikuWriter = (overrides: Partial<Writer> = {}): Writer => ({ runtime: 'claude-code', provider: 'claude-code', modelId: 'haiku', modelName: 'Claude Haiku (latest)', thinkingLevel: null, fellBack: false, ...overrides })
export const automaticSettings = (writer: Writer | null = haikuWriter()): Settings => ({ selection: null, writer })

// A fake server for writing with AI: keeps the setting in memory and records every request. A PUT answers with the new selection and the
// writer `writerFor` gives for it (by default the selection itself, written as Claude Code would say it). A draft is answered by `draft`,
// which may wait (msw's delay) so a test can stop it; by default it writes "Written: <request>".
export function textGenerationBackend(
  init: {
    settings?: Settings
    writerFor?: (selection: Schemas['TextGenerationSelection'] | null) => Writer | null
    draft?: (body: Schemas['PromptDraftRequest'], request: Request) => Response | Promise<Response>
  } = {},
) {
  const state = {
    settings: init.settings ?? automaticSettings(),
    puts: [] as Schemas['TextGenerationSettingsPut'][],
    drafts: [] as Schemas['PromptDraftRequest'][],
    aborted: 0,
  }
  const writerFor =
    init.writerFor ?? ((selection) => (selection ? haikuWriter({ provider: selection.provider, modelId: selection.modelId, modelName: selection.modelId, thinkingLevel: selection.thinkingLevel ?? null }) : haikuWriter()))
  const draft = init.draft ?? ((body) => HttpResponse.json({ text: `Written: ${body.request}`, writer: state.settings.writer ?? haikuWriter() }))
  const handlers = [
    http.get(apiUrl('/text-generation-settings'), () => HttpResponse.json(state.settings)),
    http.put(apiUrl('/text-generation-settings'), async ({ request }) => {
      const body = (await request.json()) as Schemas['TextGenerationSettingsPut']
      state.puts.push(body)
      state.settings = { selection: body.selection, writer: writerFor(body.selection) }
      return HttpResponse.json(state.settings)
    }),
    http.post(apiUrl('/prompt-drafts'), async ({ request }) => {
      const body = (await request.json()) as Schemas['PromptDraftRequest']
      state.drafts.push(body)
      request.signal.addEventListener('abort', () => (state.aborted += 1))
      return draft(body, request)
    }),
  ]
  return { state, handlers }
}

// A draft that never comes back on its own (until the request is aborted), for the writing state and Stop.
export const neverAnswers = async () => {
  await delay('infinite')
  return HttpResponse.json({})
}
