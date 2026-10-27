// 在 npm run build 之后检查 dist/：不能出现外部字体地址，@font-face 只能引用同源文件，
// 每款字体的 OFL.txt 都随产物分发。
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = fileURLToPath(new URL('../', import.meta.url))
const distDir = join(webDir, 'dist')
const lock = JSON.parse(readFileSync(join(webDir, 'scripts/fonts/fonts.lock.json'), 'utf8'))
const problems = []

function filesUnder(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name)
    return statSync(path).isDirectory() ? filesUnder(path) : [path]
  })
}

if (!existsSync(distDir)) {
  console.error('check-dist: 没有 dist/，请先运行 npm run build')
  process.exit(1)
}

const forbidden = /fonts\.googleapis\.com|fonts\.gstatic\.com/
for (const file of filesUnder(distDir).filter((path) => /\.(html|css|js)$/.test(path))) {
  const text = readFileSync(file, 'utf8')
  const name = relative(distDir, file)
  if (forbidden.test(text)) problems.push(`${name} 中出现了外部字体地址`)
  if (file.endsWith('.css')) {
    for (const [face] of text.matchAll(/@font-face\s*{[^}]*}/g)) {
      if (/url\(\s*['"]?(https?:)?\/\//.test(face)) problems.push(`${name} 的 @font-face 引用了外部地址`)
    }
  }
}

for (const font of lock.fonts) {
  if (!existsSync(join(distDir, 'fonts', font.id, 'OFL.txt'))) problems.push(`产物中缺少 fonts/${font.id}/OFL.txt`)
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`check-dist: ${problem}`)
  process.exit(1)
}
console.log('check-dist: 产物中没有外部字体地址，许可证文件齐全')
