import { api, ApiError, call, rawBody, REQUEST_ID_HEADER, toApiError, type Schemas } from './client'
import { config } from '@/config'
import type { FolderSource, UploadItem } from '@/features/folders/folder-source'
import { joinPath } from '@/features/folders/folder-source'

// Three sets of folder endpoints (console only): the Agent's folder, the project's shared folder and the global shared folder; same
// endpoints, different prefixes.
export type FolderScope = { kind: 'agent'; agentId: string } | { kind: 'project'; projectId: string } | { kind: 'global' }

type Entry = Schemas['FolderEntry']

const prefixOf = (scope: FolderScope) =>
  scope.kind === 'agent' ? `/agents/${encodeURIComponent(scope.agentId)}/folder` : scope.kind === 'project' ? `/projects/${encodeURIComponent(scope.projectId)}/context-folder` : '/context-folder'
const contentURL = (scope: FolderScope, path: string, create?: boolean) =>
  `${config.apiBase}${prefixOf(scope)}/content?${new URLSearchParams({ path, ...(create === undefined ? {} : { create: String(create) }) })}`

// Write files: PUT the raw bytes for each path one by one; the server creates parent folders automatically. XHR is used to get upload
// progress.
function putContent(scope: FolderScope, path: string, body: Blob, create: boolean, onProgress?: (loaded: number) => void): Promise<Entry> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', contentURL(scope, path, create))
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    if (onProgress) xhr.upload.onprogress = (event) => onProgress(event.loaded)
    xhr.onload = () => {
      let parsed: unknown = xhr.responseText
      try { parsed = xhr.responseText ? JSON.parse(xhr.responseText) : null } catch { /* Error bodies that are not JSON are kept as they are */ }
      if (xhr.status >= 200 && xhr.status < 300) resolve(parsed as Entry)
      else reject(toApiError(xhr.status, parsed, xhr.getResponseHeader(REQUEST_ID_HEADER)))
    }
    xhr.onerror = () => reject(new ApiError({ status: 0, code: 'unreachable' }))
    xhr.send(body)
  })
}

const LIST_LIMIT = 500

export function folderSource(scope: FolderScope): FolderSource {
  const key = scope.kind === 'agent' ? `agent:${scope.agentId}` : scope.kind === 'project' ? `project:${scope.projectId}` : 'global'
  const query = (path: string, cursor: string | null) => ({ path, limit: LIST_LIMIT, ...(cursor ? { cursor } : {}) })
  return {
    key,
    list: (path, cursor) =>
      scope.kind === 'agent' ? call(api.GET('/agents/{agentId}/folder/entries', { params: { path: { agentId: scope.agentId }, query: query(path, cursor) } }))
      : scope.kind === 'project' ? call(api.GET('/projects/{projectId}/context-folder/entries', { params: { path: { projectId: scope.projectId }, query: query(path, cursor) } }))
      : call(api.GET('/context-folder/entries', { params: { query: query(path, cursor) } })),
    read: async (path) => {
      const params = { query: { path } }
      const blob = scope.kind === 'agent' ? await call(api.GET('/agents/{agentId}/folder/content', { params: { ...params, path: { agentId: scope.agentId } }, parseAs: 'blob' }))
        : scope.kind === 'project' ? await call(api.GET('/projects/{projectId}/context-folder/content', { params: { ...params, path: { projectId: scope.projectId } }, parseAs: 'blob' }))
        : await call(api.GET('/context-folder/content', { params, parseAs: 'blob' }))
      return blob as unknown as Blob
    },
    write: (path, body, create) => {
      const blob = typeof body === 'string' ? new Blob([body]) : body
      const params = { query: { path, create } }
      return scope.kind === 'agent' ? call(api.PUT('/agents/{agentId}/folder/content', { params: { ...params, path: { agentId: scope.agentId } }, ...rawBody(blob) }))
        : scope.kind === 'project' ? call(api.PUT('/projects/{projectId}/context-folder/content', { params: { ...params, path: { projectId: scope.projectId } }, ...rawBody(blob) }))
        : call(api.PUT('/context-folder/content', { params, ...rawBody(blob) }))
    },
    mkdir: (path) =>
      scope.kind === 'agent' ? call(api.POST('/agents/{agentId}/folder/directories', { params: { path: { agentId: scope.agentId } }, body: { path } }))
      : scope.kind === 'project' ? call(api.POST('/projects/{projectId}/context-folder/directories', { params: { path: { projectId: scope.projectId } }, body: { path } }))
      : call(api.POST('/context-folder/directories', { body: { path } })),
    move: (from, to) =>
      scope.kind === 'agent' ? call(api.POST('/agents/{agentId}/folder/move', { params: { path: { agentId: scope.agentId } }, body: { from, to } }))
      : scope.kind === 'project' ? call(api.POST('/projects/{projectId}/context-folder/move', { params: { path: { projectId: scope.projectId } }, body: { from, to } }))
      : call(api.POST('/context-folder/move', { body: { from, to } })),
    remove: async (path) => {
      const params = { query: { path } }
      if (scope.kind === 'agent') await call(api.DELETE('/agents/{agentId}/folder/entries', { params: { ...params, path: { agentId: scope.agentId } } }))
      else if (scope.kind === 'project') await call(api.DELETE('/projects/{projectId}/context-folder/entries', { params: { ...params, path: { projectId: scope.projectId } } }))
      else await call(api.DELETE('/context-folder/entries', { params }))
    },
    // An upload overwrites a file with the same name (create=false): like saving in place, the last write wins.
    upload: async (target: string, item: UploadItem, onProgress) => { await putContent(scope, joinPath(target, item.path), item.file, false, onProgress) },
    contentURL: (path) => contentURL(scope, path),
    ...(scope.kind === 'agent'
      ? {
          git: {
            start: (input) => call(api.POST('/agents/{agentId}/folder/git-pulls', { params: { path: { agentId: scope.agentId } }, body: { actionId: crypto.randomUUID(), ...input } })),
            list: async () => (await call(api.GET('/agents/{agentId}/folder/git-pulls', { params: { path: { agentId: scope.agentId }, query: { limit: 20 } } }))).pulls,
          },
        }
      : {}),
  }
}
