// 检查组件代码中直接书写的中文界面文字：界面文案必须来自 src/i18n/。
// 只检查字符串、模板字符串与 JSX 文字，注释不受限制。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const CJK = /[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uff00-\uffef]/

const IGNORED = [
  (path) => path.split(sep).includes('i18n'),
  (path) => path.split(sep)[0] === 'test',
  (path) => /\.test\.tsx?$/.test(path),
  (path) => path.endsWith('.gen.ts'),
  // 服务端协议里的固定文字，用于识别旧服务端，不是界面文案。
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
      // 一个模板字符串只报告一次，插值部分仍要检查。
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
    for (const hit of findUiText(path, readFileSync(path, 'utf8'))) problems.push({ file: path, ...hit })
  }
  return problems
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = process.argv[2] ?? fileURLToPath(new URL('../src', import.meta.url))
  const problems = checkDir(dir)
  for (const p of problems) console.error(`${p.file}:${p.line}: 直接书写的界面文字「${p.text}」，请改用 src/i18n/ 中的文案`)
  if (problems.length > 0) process.exit(1)
}
