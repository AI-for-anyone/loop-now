import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { invalidateAgent } from '@/api/queries'

// Submit a credential: the request and its state live only inside the component and never enter the query cache; on success, refresh
// the Agent's credential metadata.
export function useSecretSubmit(agentId: string) {
  const client = useQueryClient()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const submit = async (request: () => Promise<unknown>): Promise<boolean> => {
    setPending(true)
    setError(null)
    try {
      await request()
      await invalidateAgent(client, agentId)
      return true
    } catch (e) {
      setError(e)
      return false
    } finally {
      setPending(false)
    }
  }
  // When local validation blocks a submit, the server error from the previous submit may no longer match the edited input, so clear it first.
  const clearError = () => setError(null)
  return { submit, pending, error, clearError }
}
