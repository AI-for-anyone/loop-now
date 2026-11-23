// Fails when console code compares a runtime id ('pi', 'claude-code', ...) instead of asking the runtime lookup for the
// runtime's name or one of its capabilities: adding a runtime must not mean adding branches across the console.
//
// A comparison counts when one side is a runtime id (a string literal, or a constant whose type is one) and the other
// side holds a runtime: its type is the API's AgentRuntime, or wider (every runtime id fits in it), or a plain string.
// Checked: ===, !==, == and !=, and the case labels of a switch. A value of another type that shares a word with a
// runtime id, such as a command's source ('platform' | 'pi'), is not a runtime and is not reported.
// The runtime ids are read from the generated API types, so a new runtime is covered without changing this check.
// Checked files are those of tsconfig.app.json (so not tests or test helpers), except the lookup itself.
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
// The one file that may compare runtime ids.
export const LOOKUP = 'src/lib/runtime.ts'
const SCHEMA = 'src/api/schema.gen.ts'
const EQUALITY = new Set([ts.SyntaxKind.EqualsEqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsEqualsToken, ts.SyntaxKind.EqualsEqualsToken, ts.SyntaxKind.ExclamationEqualsToken])

function loadProgram(root) {
  const fail = (diagnostic) => { throw new Error(ts.flattenDiagnosticMessageText(diagnostic.messageText, '\n')) }
  const parsed = ts.getParsedCommandLineOfConfigFile(path.join(root, 'tsconfig.app.json'), {}, { ...ts.sys, onUnRecoverableConfigFileDiagnostic: fail })
  if (!parsed) throw new Error(`${root}/tsconfig.app.json could not be read`)
  return ts.createProgram({ rootNames: parsed.fileNames, options: { ...parsed.options, noEmit: true } })
}

// The members of components['schemas']['AgentRuntime'] in the generated API types.
function runtimeIds(program, checker, root) {
  const schema = program.getSourceFile(path.join(root, SCHEMA))
  const components = schema?.statements.find((node) => ts.isInterfaceDeclaration(node) && node.name.text === 'components')
  if (!components) throw new Error(`${SCHEMA}: interface components not found`)
  const property = (type, name) => {
    const symbol = checker.getPropertyOfType(type, name)
    if (!symbol) throw new Error(`${SCHEMA}: ${name} not found`)
    return checker.getTypeOfSymbolAtLocation(symbol, components)
  }
  const runtime = property(property(checker.getTypeAtLocation(components.name), 'schemas'), 'AgentRuntime')
  const ids = (runtime.isUnion() ? runtime.types : [runtime]).filter((type) => type.isStringLiteral()).map((type) => type.value)
  if (ids.length === 0) throw new Error(`${SCHEMA}: AgentRuntime has no string members`)
  return ids
}

// Returns the runtime ids and every comparison of one outside the lookup, as { file, line, column, id, text }.
export function findRuntimeComparisons(root = web) {
  const program = loadProgram(root)
  const checker = program.getTypeChecker()
  const ids = runtimeIds(program, checker, root)
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
    const report = (node, id) => {
      const { line, character } = file.getLineAndCharacterOfPosition(node.getStart(file))
      found.push({ file: relative, line: line + 1, column: character + 1, id, text: node.getText(file).replace(/\s+/g, ' ') })
    }
    const visit = (node) => {
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
  for (const item of found) console.error(`${item.file}:${item.line}:${item.column}: compares the runtime id '${item.id}': ${item.text}`)
  if (found.length > 0) {
    console.error(`\n${found.length} comparison(s) of a runtime id. Ask the runtime lookup (${LOOKUP}) for the runtime's name or a capability instead.`)
    process.exit(1)
  }
  console.log(`check-runtime-ids: no runtime id (${ids.join(', ')}) is compared outside ${LOOKUP}.`)
}
