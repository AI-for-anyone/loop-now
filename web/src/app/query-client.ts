import { QueryClient } from '@tanstack/react-query'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 控制台只访问本机服务端，浏览器判断外网离线时不暂停请求。
        networkMode: 'always',
        // 服务端不可达或资源不存在时不重试，直接显示原因。
        retry: (count, error) => count < 2 && !('status' in error && (error.status === 0 || error.status === 404)),
      },
      mutations: { networkMode: 'always' },
    },
  })
}
