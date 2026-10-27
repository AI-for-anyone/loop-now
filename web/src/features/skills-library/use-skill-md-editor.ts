import { useState } from 'react'

// 抽屉中 SKILL.md 的编辑状态：编辑器在抽屉正文，保存按钮在抽屉底栏，所以状态放在两者共同的组件里。
// 保存失败时保留已编辑的内容；resetKey 变化（服务端内容已更新）时用新的 initial 重置，
// 后台刷新但内容未变时不重置，避免丢失未保存的编辑。
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
