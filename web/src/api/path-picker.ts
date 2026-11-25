import { api, call, type Schemas } from './client'
import { useContextInfo } from './queries'

// Choosing a path in a browser: Loop Now's server shows this computer's own dialog (the console port listens only on this computer, so it
// is the screen the browser is on). The default context, read once at start, says whether it can (Context.pathPicker).
export function useServerPathPicker(enabled: boolean): boolean {
  const context = useContextInfo()
  return enabled && context.data?.pathPicker === true
}

// Answered when the person chooses (the full path) or cancels (null), with no time limit. Aborting the signal closes the dialog at once;
// the promise then rejects with the DOMException named AbortError.
export const pickPathOnServer = async (body: Schemas['PathPickRequest'], signal?: AbortSignal): Promise<string | null> =>
  (await call(api.POST('/path-picks', { body, signal }))).path
