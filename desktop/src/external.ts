import { join } from 'node:path'

// How external identities and loopctl connect. The desktop app's external TLS port is only on 127.0.0.1, and its certificate is a
// self-signed one the server maintains itself: the shell picks the directory (userData/tls), the server writes cert.pem and key.pem into it
// (see internal/server/selfsigned), and loopctl uses cert.pem as --ca-file.
export const certFile = 'cert.pem'

export function externalCertPath(tlsDir: string): string {
  return join(tlsDir, certFile)
}

// The command that configures the token into loopctl's default profile; the token is read from stdin, so it does not stay in the shell
// history. The path is wrapped in double quotes (macOS's "Application Support" and Windows's "Loop Now" both contain spaces; double quotes
// work in zsh, bash, PowerShell and cmd).
export function loopctlConfigCommand(externalUrl: string, caFile: string): string {
  return `loopctl config set default --server ${externalUrl} --ca-file "${caFile}" --token-stdin`
}
