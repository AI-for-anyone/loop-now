// Checks that code comments are written in English, so that contributors who do not read Chinese can follow the code.
// Scans web/, ../desktop and ../plugins: comments in TS/JS (found with the TypeScript parser), CSS and HTML block
// comments, Go comments, and "#" comments in shell, Python and .env example files.
// Only comments are checked. UI copy lives in string literals (src/i18n/ and test assertions), so it is never flagged.
// A comment may quote Chinese UI text inside quotation marks (“…”, 「…」, 『…』 or "…"; '…' only around text without
// ASCII letters, so apostrophes do not hide anything), e.g. `the “不可撤销” (Irreversible) stamp`.
// Anything else that is CJK, including full-width punctuation, is reported. Python docstrings are strings, not comments,
// and are not checked.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import { basename, extname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const CJK = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff00-\uffef]/
const QUOTED = /“[^”]*”|「[^」]*」|『[^』]*』|"[^"\n]*"|'[^'\nA-Za-z]*'/g
const SKIPPED_DIRS = new Set(['node_modules', 'dist', 'coverage', '.vite', 'test-results', '.local-build', 'release', 'out'])
const SCRIPT_EXTENSIONS = new Set(['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'])
const HASH_EXTENSIONS = new Set(['.py', '.sh', '.example'])

function kindOf(path, source) {
  const ext = extname(path)
  if (path.endsWith('.gen.ts')) return null
  if (SCRIPT_EXTENSIONS.has(ext)) return 'script'
  if (ext === '.css') return 'css'
  if (ext === '.html') return 'html'
  if (ext === '.go') return 'go'
  if (HASH_EXTENSIONS.has(ext) || basename(path) === 'requirements.txt' || (ext === '' && source.startsWith('#!'))) return 'hash'
  return null
}

// Every comment range in a TS/JS file: the leading and trailing trivia of every node and token, deduplicated.
// This also finds comments inside JSX expressions ({/* … */}); JSX text itself never holds comments.
function scriptComments(path, source) {
  const scriptKind = path.endsWith('.tsx') || path.endsWith('.jsx') ? ts.ScriptKind.TSX : path.endsWith('.ts') ? ts.ScriptKind.TS : ts.ScriptKind.JS
  const file = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, scriptKind)
  const ranges = new Map()
  const add = (found) => { for (const range of found ?? []) ranges.set(range.pos, range) }
  const visit = (node) => {
    add(ts.getLeadingCommentRanges(source, node.pos))
    add(ts.getTrailingCommentRanges(source, node.end))
    for (const child of node.getChildren(file)) visit(child)
  }
  visit(file)
  return [...ranges.values()]
}

// The other file kinds use a small lexer that skips quoted strings, so "#" or "/*" inside a string is not a comment.
function lexedComments(source, kind) {
  const ranges = []
  let i = 0
  let quote = null
  const until = (start, marker) => {
    const end = source.indexOf(marker, start)
    return end < 0 ? source.length : end + (marker === '\n' ? 0 : marker.length)
  }
  while (i < source.length) {
    const c = source[i]
    if (quote) {
      if (c === '\\') i += 2
      else { if (c === quote) quote = null; i++ }
      continue
    }
    let end = -1
    if ((kind === 'css' || kind === 'html' || kind === 'go') && source.startsWith('/*', i)) end = until(i + 2, '*/')
    else if (kind === 'html' && source.startsWith('<!--', i)) end = until(i + 4, '-->')
    else if (kind === 'go' && source.startsWith('//', i)) end = until(i, '\n')
    else if (kind === 'hash' && c === '#' && (i === 0 || /\s/.test(source[i - 1]))) end = until(i, '\n')
    if (end >= 0) { ranges.push({ pos: i, end }); i = end; continue }
    if (kind !== 'html' && (c === '"' || c === "'" || (kind === 'go' && c === '`'))) quote = c
    i++
  }
  return ranges
}

// Lines of comments that still contain Chinese outside quotation marks: [{ line, text }].
export function findChineseComments(path, source) {
  const kind = kindOf(path, source)
  if (!kind) return []
  const ranges = kind === 'script' ? scriptComments(path, source) : lexedComments(source, kind)
  const hits = []
  for (const range of ranges) {
    const firstLine = source.slice(0, range.pos).split('\n').length
    source.slice(range.pos, range.end).split('\n').forEach((text, offset) => {
      if (CJK.test(text.replace(QUOTED, ''))) hits.push({ line: firstLine + offset, text: text.trim() })
    })
  }
  return hits.sort((a, b) => a.line - b.line)
}

function listFiles(dir) {
  const files = []
  for (const name of readdirSync(dir)) {
    if (SKIPPED_DIRS.has(name)) continue
    const path = join(dir, name)
    if (statSync(path).isDirectory()) files.push(...listFiles(path))
    else files.push(path)
  }
  return files
}

// Checks every directory that exists (desktop/ and plugins/ may be missing in a partial checkout).
export function checkDirs(dirs) {
  const problems = []
  for (const dir of dirs.filter((d) => existsSync(d))) {
    for (const path of listFiles(dir)) {
      const source = readFileSync(path, 'utf8')
      for (const hit of findChineseComments(path, source)) problems.push({ file: path, ...hit })
    }
  }
  return problems
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const web = fileURLToPath(new URL('..', import.meta.url))
  const dirs = process.argv.length > 2 ? process.argv.slice(2) : [web, join(web, '..', 'desktop'), join(web, '..', 'plugins')]
  const problems = checkDirs(dirs)
  for (const p of problems) console.error(`${relative(process.cwd(), p.file)}:${p.line}: comment is not in English: ${p.text}`)
  if (problems.length > 0) {
    console.error(`${problems.length} comment line(s) contain Chinese. Write comments in English; quote Chinese UI text as “…” followed by the English wording.`)
    // exitCode rather than exit(): on macOS, output to a pipe is asynchronous and exit() could cut the list short.
    process.exitCode = 1
  }
}
