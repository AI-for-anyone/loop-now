import { useState } from 'react'

// Edit state of SKILL.md in the drawer: the editor is in the drawer body and the save button in the drawer footer, so the state
// lives in their common parent component.
// A failed save keeps the edited content; when resetKey changes (the server content was updated) it resets to the new initial,
// but a background refresh with unchanged content doesn't reset it, so unsaved edits aren't lost.
export function useSkillMdEditor(initial: string, onSave: (content: string) => Promise<unknown>, resetKey?: string) {
  const [content, setContent] = useState(initial)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [key, setKey] = useState(resetKey)
  if (resetKey !== key) {
    setKey(resetKey)
    setContent(initial)
    setError(null)
  }

  const save = async () => {
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

  return { content, setContent, pending, error, canSave: !pending && content.trim() !== '', save }
}
