import { useMemo } from 'react'
import type { Schemas } from '@/api/client'
import {
  agentFolderContentURL,
  agentFolderRawURL,
  checkAgentFolderDirectory,
  folderRoots,
  taskNodeFolderContentURL,
  taskNodeFolderRawURL,
  useAgentFolderLocation,
  useTaskNodeFolderLocation,
} from '@/api/folders'
import { useContextInfo } from '@/api/queries'
import { DEFAULT_IMAGE_PREVIEW_MAX_BYTES, type ReplyFolderContext } from './local-images'

export function useAgentReplyFolder(
  agentId: string | undefined,
  folderTabPath?: string,
  maxBytesOverride?: number,
): ReplyFolderContext | undefined {
  const folderLocation = useAgentFolderLocation(agentId)
  const context = useContextInfo()
  const maxBytes = maxBytesOverride ?? context.data?.limits.textEdit.maxBytes ?? DEFAULT_IMAGE_PREVIEW_MAX_BYTES
  const rootsReady = folderLocation.isSuccess || folderLocation.isError
  return useMemo<ReplyFolderContext | undefined>(() => {
    if (!agentId) return undefined
    return {
      roots: folderRoots(folderLocation.data),
      rootsReady,
      contentURL: (path) => agentFolderContentURL(agentId, path),
      rawURL: (path) => agentFolderRawURL(agentId, path),
      folderLink: folderTabPath ? (path) => (path ? `${folderTabPath}?${new URLSearchParams({ path })}` : folderTabPath) : undefined,
      checkDirectory: (path, signal) => checkAgentFolderDirectory(agentId, path, signal),
      maxBytes,
    }
  }, [agentId, folderLocation.data, rootsReady, folderTabPath, maxBytes])
}

export function useTaskNodeReplyFolder(
  taskId: string | undefined,
  nodeRunId: string | undefined,
  maxBytesOverride?: number,
): ReplyFolderContext | undefined {
  const folderLocation = useTaskNodeFolderLocation(taskId, nodeRunId)
  const context = useContextInfo()
  const maxBytes = maxBytesOverride ?? context.data?.limits.textEdit.maxBytes ?? DEFAULT_IMAGE_PREVIEW_MAX_BYTES
  const rootsReady = folderLocation.isSuccess || folderLocation.isError
  return useMemo<ReplyFolderContext | undefined>(() => {
    if (!taskId || !nodeRunId) return undefined
    return {
      roots: folderRoots(folderLocation.data),
      rootsReady,
      contentURL: (path) => taskNodeFolderContentURL(taskId, nodeRunId, path),
      rawURL: (path) => taskNodeFolderRawURL(taskId, nodeRunId, path),
      maxBytes,
    }
  }, [taskId, nodeRunId, folderLocation.data, rootsReady, maxBytes])
}

// Resolve the folder context for a read-only Inbox message:
// - When sent by an Agent, use that sender's task node folder (if sent from a task instance) or regular Agent folder.
// - Otherwise, if viewed inside a regular Agent's inbox or addressed to an Agent, use that Agent's folder.
export function useInboxMessageReplyFolder(
  message: Schemas['InboxMessage'] | undefined,
  owner?: { kind: 'agent'; agentId: string } | { kind: 'identity'; identityId: string },
  enabled = true,
): ReplyFolderContext | undefined {
  let agentId: string | undefined
  let taskId: string | undefined
  let nodeRunId: string | undefined

  if (enabled && message) {
    if (message.from.kind === 'agent') {
      if (message.task?.nodeRunId) {
        taskId = message.task.taskId
        nodeRunId = message.task.nodeRunId
      } else if (message.from.id) {
        agentId = message.from.id
      }
    } else if (owner?.kind === 'agent') {
      agentId = owner.agentId
    } else if (message.to.kind === 'agent') {
      if (message.task?.nodeRunId) {
        taskId = message.task.taskId
        nodeRunId = message.task.nodeRunId
      } else if (message.to.id) {
        agentId = message.to.id
      }
    }
  }

  const agentFolder = useAgentReplyFolder(agentId)
  const taskNodeFolder = useTaskNodeReplyFolder(taskId, nodeRunId)
  return taskNodeFolder ?? agentFolder
}
