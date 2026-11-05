// 用 test/fakeserver 搭一个最小的"服务端发行包平台目录"，给没有真服务端包时的打包（dist:win）和 CI 用：
//   <输出目录>/loop-server[.exe]                            假服务端（按 --os 交叉编译）
//   <输出目录>/loopctl-dist/0.0.0/manifest.json             after-pack 需要的版本目录
//   <输出目录>/loopctl-dist/0.0.0/loopctl-<os>-<arch>[.exe]  假服务端的副本充当 loopctl
//   node scripts/fake-bundle.js <输出目录> [--os windows|darwin|linux] [--arch amd64|arm64]
const { execFileSync } = require('node:child_process')
const { copyFileSync, mkdirSync, rmSync, writeFileSync } = require('node:fs')
const { join, resolve } = require('node:path')

const args = process.argv.slice(2)
const out = args[0] && !args[0].startsWith('--') ? resolve(args[0]) : undefined
if (!out) {
  console.error('用法：node scripts/fake-bundle.js <输出目录> [--os windows|darwin|linux] [--arch amd64|arm64]')
  process.exit(2)
}
const option = (name, fallback) => {
  const i = args.indexOf(`--${name}`)
  return i >= 0 && args[i + 1] ? args[i + 1] : fallback
}
const goos = option('os', { win32: 'windows', darwin: 'darwin' }[process.platform] ?? 'linux')
const goarch = option('arch', process.arch === 'arm64' ? 'arm64' : 'amd64')
const exe = goos === 'windows' ? '.exe' : ''

rmSync(out, { recursive: true, force: true })
mkdirSync(out, { recursive: true })
const server = join(out, `loop-server${exe}`)
execFileSync('go', ['build', '-o', server, './test/fakeserver'], {
  cwd: join(__dirname, '..'),
  stdio: 'inherit',
  env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' },
})
const dist = join(out, 'loopctl-dist', '0.0.0')
mkdirSync(dist, { recursive: true })
copyFileSync(server, join(dist, `loopctl-${goos}-${goarch}${exe}`))
writeFileSync(join(dist, 'manifest.json'), JSON.stringify({ schemaVersion: 1, program: 'loopctl', version: '0.0.0', fake: true }, null, 2) + '\n')
console.log(`假发行包：${out}（${goos}/${goarch}）`)
