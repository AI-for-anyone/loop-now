// Builds a minimal "server release bundle platform directory" from test/fakeserver, for packaging when there is no real server bundle
// (dist:win) and for CI:
//   <output-dir>/loop-server[.exe]                            fake server (cross-compiled for --os)
//   <output-dir>/loopctl-dist/0.0.0/manifest.json             the version directory after-pack needs
//   <output-dir>/loopctl-dist/0.0.0/loopctl-<os>-<arch>[.exe]  a copy of the fake server standing in for loopctl
//   node scripts/fake-bundle.js <output-dir> [--os windows|darwin|linux] [--arch amd64|arm64]
const { execFileSync } = require('node:child_process')
const { copyFileSync, mkdirSync, rmSync, writeFileSync } = require('node:fs')
const { join, resolve } = require('node:path')

const args = process.argv.slice(2)
const out = args[0] && !args[0].startsWith('--') ? resolve(args[0]) : undefined
if (!out) {
  console.error('Usage: node scripts/fake-bundle.js <output-dir> [--os windows|darwin|linux] [--arch amd64|arm64]')
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
console.log(`Fake bundle: ${out} (${goos}/${goarch})`)
