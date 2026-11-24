// Fails when console code compares a runtime id ('pi', 'claude-code', ...) instead of asking the runtime lookup for the
// runtime's name or one of its capabilities: adding a runtime must not mean adding branches across the console.
//
// A comparison counts when one side is a runtime id (a string literal, or a constant whose type is one) and the other
// side holds a runtime: its type is the API's AgentRuntime, or wider (every runtime id fits in it), or a plain string.
// Checked: ===, !==, == and !=, and the case labels of a switch. A value of another type that shares a word with a
// runtime id, such as a command's source ('platform' | 'pi'), is not a runtime and is not reported.
// The contract's AgentRuntime is a plain string (each local ACP command a person adds is a runtime of its own), so the ids are
// read from the lookup's list of the built-in runtimes (BUILT_IN_RUNTIMES in src/lib/runtime.ts): a new built-in runtime is
// covered once it is listed there, and the check stops if the list is gone rather than checking nothing.
// The runtimes people add are named acp-<command id>. Telling them apart by that prefix is a comparison too: any literal
// starting with it (a string, a template, a pattern) is reported; the console asks the lookup instead (acpCommandId).
// Checked files are those of tsconfig.app.json (so not tests or test helpers), except the lookup itself.
// Runs as part of npm run lint.
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// The one file that may compare runtime ids.
export const LOOKUP = 'src/lib/runtime.ts'
const SCHEMA = 'src/api/schema.gen.ts'
const LIST = 'BUILT_IN_RUNTIMES'
// The prefix of the runtimes people add (contract AgentRuntime: acp- followed by the command's id).
const ADDED_PREFIX = 'acp-'
const EQUALITY = new Set([ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken])

function loadProgram(root) {
  const fail = (diagnostic) => { throw new Error(ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')) }
  const parsed = ts.getParsedCommandLineOfConfigFile(path.join(root, 'tsconfig.app.json'), {}, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: fail })
  if (!parsed) throw new Error(`${root}/tsconfig.app.json could not be read`)
  return ts.createProgram({ rootNames: parsed.fileNames, options: { ...parsed.options, noEmit: true } })
}

// The ids in the lookup's `export const BUILT_IN_RUNTIMES = [...] as const`.
function runtimeIds(program, root) {
  const lookup = program.getSourceFile(path.join(root, LOOKUP))
  let list
  const visit = (node) => {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === LIST) list = node.initializer
    else ts.forEachChild(node, visit)
  }
  if (lookup) visit(lookup)
  while (list && (ts.isAsExpression(list) || ts.isSatisfiesExpression(list) || ts.isParenthesizedExpression(list))) list = list.expression
  if (!list || !ts.isArrayLiteralExpression(list)) throw new Error(`${LOOKUP}: ${LIST} (an array of the built-in runtime ids) not found`)
  const ids = list.elements.filter((element) => ts.isStringLiteral(element)).map((element) => element.text)
  if (ids.length === 0) throw new Error(`${LOOKUP}: ${LIST} has no runtime ids`)
  return ids
}

// A literal naming the prefix of the runtimes people add: its text, or null.
function addedPrefix(node, file) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node)) return node.text.startsWith(ADDED_PREFIX) ? node.getText(file) : null
  if (node.kind === ts.SyntaxKind.RegularExpressionLiteral) return node.getText(file).includes(ADDED_PREFIX) ? node.getText(file) : null
  return null
}

// Returns the runtime ids and every comparison of one outside the lookup, as { file, line, column, id, text }.
export function findRuntimeComparisons(root = web) {
  const program = loadProgram(root)
  const checker = program.getTypeChecker()
  const ids = runtimeIds(program, root)
  const idOf = (node) => {
    const type = checker.getTypeAtLocation(node)
    return type.isStringLiteral() && ids.includes(type.value) ? type.value : null
  }
  const holdsRuntime = (node) => {
    const type = checker.getNonNullableType(checker.getTypeAtLocation(node))
    const parts = type.isUnion() ? type.types : [type]
    if (parts.some((part) => part.flags & ts.TypeFlags.String)) return true
    const values = new Set(parts.filter((part) => part.isStringLiteral()).map((part) => part.value))
    return ids.every((id) => values.has(id))
  }
  const found = []
  for (const file of program.getSourceFiles()) {
    const relative = path.relative(root, file.fileName).split(path.sep).join('/')
    if (file.isDeclarationFile || relative.startsWith('..') || relative === SCHEMA || relative === LOOKUP) continue
    const report = (node, id, text = node.getText(file)) => {
      const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file))
      found.push({ file: relative, line: line + 1, column: character + 1, id, text: text.replace(/\s+/g, ' ') })
    }
    const visit = (node) => {
      const prefix = addedPrefix(node, file)
      if (prefix) report(node, ADDED_PREFIX, prefix)
      if (ts.isBinaryExpression(node) && EQUALITY.has(node.operatorToken.kind)) {
        const left = idOf(node.left), right = idOf(node.right)
        if (left && !right && holdsRuntime(node.right)) report(node, left)
        if (right && !left && holdsRuntime(node.left)) report(node, right)
      } else if (ts.isCaseClause(node)) {
        const id = idOf(node.expression)
        if (id && holdsRuntime(node.parent.parent.expression)) report(node.expression, id)
      }
      ts.forEachChild(node, visit)
    }
    visit(file)
  }
  return { ids, found }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const { ids, found } = findRuntimeComparisons()
  for (const item of found) {
    const what = item.id === ADDED_PREFIX ? `names the prefix of added runtimes '${ADDED_PREFIX}'` : `compares the runtime id '${item.id}'`
    console.error(`${item.file}:${item.line}:${item.column}: ${what}: ${item.text}`)
  }
  if (found.length > 0) {
    console.error(`\n${found.length} comparison(s) of a runtime id. Ask the runtime lookup (${LOOKUP}) for the runtime's name or a capability (an added one has acpCommandId) instead.`)
    process.exit(1)
  }
  console.log(`check-runtime-ids: no runtime id (${ids.join(', ')}) or the prefix of added runtimes ('${ADDED_PREFIX}') is compared outside ${LOOKUP}.`)
}
