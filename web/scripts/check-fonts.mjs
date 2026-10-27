// 核对仓库中的字体文件与 scripts/fonts/fonts.lock.json 的 outputs 一致，每款字体都带 OFL.txt，
// fonts.css 引用的文件都存在。字体由 scripts/fonts/build_fonts.py 生成，这里不依赖 Python。
import { createHash } from 'node:crypto'
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = fileURLToPath(new URL('../', import.meta.url))
const lock = JSON.parse(readFileSync(join(webDir, 'scripts/fonts/fonts.lock.json'), 'utf8'))
const outputs = lock.outputs ?? {}
const problems = []

const sha256 = (path) => createHash('sha256').update(readFileSync(path)).digest('hex')

function filesUnder(dir) {
  if (!existsSync(dir)) return []
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? filesUnder(path) : [path]
  })
}

if (Object.keys(outputs).length === 0) problems.push('锁文件中没有 outputs，请先运行 build_fonts.py')

for (const [path, expected] of Object.entries(outputs)) {
  const full = join(webDir, path)
  if (!existsSync(full)) problems.push(`缺少 ${path}`)
  else if (sha256(full) !== expected) problems.push(`${path} 的内容与锁文件不一致`)
}

const tracked = [...filesUnder(join(webDir, 'src/assets/fonts')), ...filesUnder(join(webDir, 'public/fonts'))]
for (const full of tracked) {
  const path = relative(webDir, full).split('\\').join('/')
  if (!(path in outputs) && !path.endsWith('.DS_Store')) problems.push(`${path} 不在锁文件中`)
}

for (const font of lock.fonts) {
  if (!(`public/fonts/${font.id}/OFL.txt` in outputs)) problems.push(`${font.family} 缺少 OFL.txt`)
}

const cssPath = join(webDir, 'src/styles/fonts.css')
const css = existsSync(cssPath) ? readFileSync(cssPath, 'utf8') : ''
for (const [, , url] of css.matchAll(/url\(\s*(['"]?)([^'")]+)\1\s*\)/g)) {
  if (/^[a-z]+:/i.test(url)) problems.push(`fonts.css 引用了外部地址 ${url}`)
  else if (!existsSync(resolve(join(webDir, 'src/styles'), url))) problems.push(`fonts.css 引用的 ${url} 不存在`)
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`check-fonts: ${problem}`)
  process.exit(1)
}
console.log(`check-fonts: ${Object.keys(outputs).length} 个文件与锁文件一致`)
