import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { useDirtyForm } from '@/features/prompts/draft-navigation'

// The form lifecycle, not an API DTO. T may only be local input; the revision and the safe review summary are supplied by the official API
// adapter layer.
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
    // Repeated clicks within the same gesture cycle must not submit a second time either; a CAS failure is never retried automatically.
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
    // Adopt only the revision the person actually reviewed; later props refreshes must not silently replace this baseline.
    setBase({ revision: review.revision, summary: review.summary })
    setConflict(false); setReview(null); setError(null)
  }
  return { draft, setDraft, base, dirty, pending, error, conflict, review, reading, readError, refresh, adopt, submit }
}
