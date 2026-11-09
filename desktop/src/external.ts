import { join } from 'node:path'

// 外部身份与 loopctl 的连接方式。桌面版的对外 TLS 端口只在 127.0.0.1 上，证书是服务端自己维护的自签证书：
// 目录由壳指定（userData/tls），服务端在里面写 cert.pem 与 key.pem（见 internal/server/selfsigned），loopctl 把 cert.pem 当 --ca-file。
export const certFile = 'cert.pem'

export function externalCertPath(tlsDir: string): string {
  return join(tlsDir, certFile)
}

// 把令牌配置进 loopctl 默认 profile 的命令；令牌从标准输入读，不会留在 shell 历史里。路径用双引号包起来（macOS 的
// "Application Support" 与 Windows 的 "Loop Now" 都带空格；双引号在 zsh、bash、PowerShell 与 cmd 里都可用）。
export function loopctlConfigCommand(externalUrl: string, caFile: string): string {
  return `loopctl config set default --server ${externalUrl} --ca-file "${caFile}" --token-stdin`
}
