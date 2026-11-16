// Check dist/ after npm run build: no external font URLs may appear, @font-face may only reference same-origin files,
// and every font's OFL.txt ships with the build output.
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
  console.error('check-dist: no dist/; run npm run build first')
  process.exit(1)
}

const forbidden = /fonts\.googleapis\.com|fonts\.gstatic\.com/
for (const file of filesUnder(distDir).filter((path) => /\.(html|css|js)$/.test(path))) {
  const text = readFileSync(file, 'utf8')
  const name = relative(distDir, file)
  if (forbidden.test(text)) problems.push(`${name} contains an external font URL`)
  if (file.endsWith('.css')) {
    for (const [face] of text.matchAll(/@font-face\s*{[^}]*}/g)) {
      if (/url\(\s*['"]?(https?:)?\/\//.test(face)) problems.push(`${name} has an @font-face that references an external URL`)
    }
  }
}

for (const font of lock.fonts) {
  if (!existsSync(join(distDir, 'fonts', font.id, 'OFL.txt'))) problems.push(`fonts/${font.id}/OFL.txt is missing from the build output`)
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`check-dist: ${problem}`)
  process.exit(1)
}
console.log('check-dist: no external font URLs in the build output, and all license files are present')
