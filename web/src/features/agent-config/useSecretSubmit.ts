import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { invalidateAgent } from '@/api/queries'

// 提交凭证：请求与状态只保存在组件内，不进入查询缓存；成功后刷新 Agent 的凭证元数据。
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
  // 本地校验拦下提交时，上一次提交的服务端错误可能已对应不上改过的输入，先清掉。
  const clearError = () => setError(null)
  return { submit, pending, error, clearError }
}
