// Checks for Chinese UI text written directly in component code: UI copy must come from src/i18n/. The English copy
// table (i18n/en-US) must not contain Chinese either.
// Only string literals, template literals and JSX text are checked; comments are not restricted here.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const CJK = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uff00-\uffef]/

const isEnglishTable = (path) => path.split(sep).includes('en-US')

const IGNORED = [
  (path) => path.split(sep).includes('i18n') && !isEnglishTable(path),
  (path) => path.split(sep)[0] === 'test',
  (path) => /\.test\.tsx?$/.test(path),
  (path) => path.endsWith('.gen.ts'),
  // Fixed text from the server protocol, used to recognize older servers; it is not UI copy.
  (path) => path === join('api', 'backend-signals.ts'),
]

function listSourceFiles(dir) {
  const files = []
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) files.push(...listSourceFiles(path))
    else if (/\.tsx?$/.test(name)) files.push(path)
  }
  return files
}

export function findUiText(path, source) {
  const file = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, path.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS)
  const hits = []
  const report = (node, text) => {
    if (!CJK.test(text)) return
    const { line } = file.getLineAndCharacterOfPosition(node.getStart(file))
    hits.push({ line: line + 1, text: text.trim() })
  }
  const visit = (node) => {
    if (ts.isTemplateExpression(node)) {
      // Report a template literal once; its interpolations are still checked.
      report(node, [node.head.text, ...node.templateSpans.map((span) => span.literal.text)].join('…'))
      for (const span of node.templateSpans) visit(span.expression)
      return
    }
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) report(node, node.text)
    else if (ts.isJsxText(node)) report(node, node.getText(file))
    ts.forEachChild(node, visit)
  }
  visit(file)
  return hits
}

export function checkDir(dir) {
  const problems = []
  for (const path of listSourceFiles(dir)) {
    const rel = relative(dir, path)
    if (IGNORED.some((ignore) => ignore(rel))) continue
    for (const hit of findUiText(path, readFileSync(path, 'utf8'))) problems.push({ file: path, english: isEnglishTable(rel), ...hit })
  }
  return problems
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = process.argv[2] ?? fileURLToPath(new URL('../src', import.meta.url))
  const problems = checkDir(dir)
  for (const p of problems)
    console.error(p.english ? `${p.file}:${p.line}: Chinese text in the English copy table: "${p.text}"` : `${p.file}:${p.line}: hard-coded UI text "${p.text}"; use copy from src/i18n/ instead`)
  if (problems.length > 0) process.exit(1)
}
