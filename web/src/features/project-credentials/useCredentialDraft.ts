import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { useDirtyForm } from '@/features/prompts/draft-navigation'

// 表单生命周期，不是 API DTO。T 只能是本地输入；版本与安全核对摘要由正式接口适配层提供。
export function useCredentialDraft<T>({ id, initial, revision, summarize, readLatest, save, onSaved }: {
  id: string
  initial: T
  revision: number
  summarize: string
  readLatest: () => Promise<{ revision: number; summary: string; deleted?: boolean }>
  save: (draft: T, revision: number) => Promise<void>
  onSaved: () => void
}) {
  const [draft, setDraft] = useState(initial)
  const [base, setBase] = useState({ revision, summary: summarize })
  const [review, setReview] = useState<{ revision: number; summary: string; deleted?: boolean } | null>(null)
  const [conflict, setConflict] = useState(false)
  const [reading, setReading] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [readError, setReadError] = useState<unknown>(null)
  const active = useRef(false)
  const mounted = useRef(true)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial)
  useDirtyForm(id, dirty || conflict)

  const refresh = async () => {
    setReading(true); setReadError(null); setReview(null)
    try { const latest = await readLatest(); if (mounted.current) setReview(latest) }
    catch (e) { if (mounted.current) setReadError(e) }
    finally { if (mounted.current) setReading(false) }
  }
  const submit = async (input: T = draft) => {
    // 同一手势周期中的重复点击也不能提交第二次；CAS失败不自动重试。
    if (active.current || conflict) return
    active.current = true; setPending(true); setError(null)
    try { await save(input, base.revision); if (mounted.current) onSaved() }
    catch (e) {
      if (!mounted.current) return
      setError(e)
      if (e instanceof ApiError && (e.status === 412 || e.code === 'precondition_failed')) {
        setConflict(true); await refresh()
      }
    } finally { active.current = false; if (mounted.current) setPending(false) }
  }
  const adopt = () => {
    if (!review || reading || review.deleted) return
    // 仅采用人实际核对的版本；后续props刷新不能静默替换该基线。
    setBase({ revision: review.revision, summary: review.summary })
    setConflict(false); setReview(null); setError(null)
  }
  return { draft, setDraft, base, dirty, pending, error, conflict, review, reading, readError, refresh, adopt, submit }
}
