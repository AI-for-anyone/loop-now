import { useState, type KeyboardEvent } from 'react'

export interface SkillMdEditorOptions {
  resetKey?: string
  requireDirty?: boolean
}

// Edit state of SKILL.md in the workspace pane: tracks dirty state, supports Cmd+S / Ctrl+S saving, and resets when resetKey
// changes (the server content was updated or another skill was selected), while preserving unsaved edits on failed background refreshes.
export function useSkillMdEditor(
  initial: string,
  onSave: (content: string) => Promise<unknown>,
  options?: string | SkillMdEditorOptions,
) {
  const { resetKey, requireDirty = false } = typeof options === 'string' ? { resetKey: options, requireDirty: false } : (options ?? {})
  const [content, setContent] = useState(initial)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [key, setKey] = useState(resetKey)

  if (resetKey !== key) {
    setKey(resetKey)
    setContent(initial)
    setError(null)
  }

  const isDirty = content !== initial
  const canSave = !pending && content.trim() !== '' && (!requireDirty || isDirty)

  const reset = () => {
    setContent(initial)
    setError(null)
  }

  const save = async () => {
    if (!canSave) return
    setPending(true)
    setError(null)
    try {
      await onSave(content)
    } catch (e) {
      setError(e)
    } finally {
      setPending(false)
    }
  }

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 's') {
      event.preventDefault()
      if (canSave) void save()
    }
  }

  return { content, setContent, pending, error, isDirty, canSave, reset, save, onKeyDown }
}
