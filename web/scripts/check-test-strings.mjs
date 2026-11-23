// Checks that the tests are written in English: test names, assertions and test data, so that contributors who do not read
// Chinese can follow them. The web tests run in the English UI; the Chinese UI is covered by a small supplement in web/src/zh-CN,
// which may use Chinese freely (see web/src/zh-CN/README.md).
//
// Checked files (TS/JS only) under web/, ../desktop and ../plugins: test files (*.test.*), files in a test/ directory (helpers and
// fixtures such as web/src/test), and the browser verification scripts and fixtures in web/scripts/ (*-app, *-browser, *-fixture and
// everything in web/scripts/integration; not the check scripts themselves). In those, a string literal,
// template literal part, regular expression or JSX text that contains Chinese (Han characters or CJK/full-width punctuation) is
// reported, unless:
//   - the file is in the Chinese supplement web/src/zh-CN/;
//   - a comment on the literal's first or last line says "check-english: zh (<reason>)", for test data that must be Chinese or
//     multibyte (a CJK name, truncation of multibyte text, a test that asserts both languages);
//   - the leading comment of the top-level statement that holds it says "check-english: zh (<reason>)" (the whole statement is
//     allowed, e.g. a table of data in both languages);
//   - the file is listed in NOT_YET_ENGLISH below, with the reason it has not been moved to English yet.
// The marker is the one the Go check (scripts/check-english) uses; here the reason in parentheses is required.
import { existsSync, readFileSync } from 'node:fs'
import { extname, join, relative, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import { CJK, listFiles, scriptComments } from './check-comments.mjs'

const MARKER = /check-english: zh \(\s*[^)\s][^)]*\)/
const SCRIPT_EXTENSIONS = new Set(['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs'])
const SUPPLEMENT = 'web/src/zh-CN/'

// Files (paths from the repository root) that still hold Chinese, each with the reason. Keep this list short and shrinking.
// The browser checks in web/scripts were not moved to English: a move must be run to be trusted, and they cannot run on a machine
// without their environment (Playwright imported from a fixed npx path, Node 24.21.0 in ~/.local, and for some a real CLI or
// download). They stay as they are until that environment is set up.
export const NOT_YET_ENGLISH = new Map([
  ['web/scripts/cli-import-app.mjs', 'browser check run by TestWebCLIImportIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/cli-verification-app.mjs', 'browser check run by TestWebCLIVerificationIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and downloads real gh, MySQL and PostgreSQL installers, none of them on this machine'],
  ['web/scripts/db-connections-app.mjs', 'browser check run by TestWebDBConnectionsIntegration, TestWebGoogleLoginIntegration, TestWebGoogleMetadataScreenshots, TestWebGoogleSAIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and psql, none of them on this machine'],
  ['web/scripts/google-login-app.mjs', 'browser check run by TestWebDBConnectionsIntegration, TestWebGoogleLoginIntegration, TestWebGoogleMetadataScreenshots, TestWebGoogleSAIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and psql, none of them on this machine'],
  ['web/scripts/google-metadata-app.mjs', 'browser check run by TestWebDBConnectionsIntegration, TestWebGoogleLoginIntegration, TestWebGoogleMetadataScreenshots, TestWebGoogleSAIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and psql, none of them on this machine'],
  ['web/scripts/google-sa-app.mjs', 'browser check run by TestWebDBConnectionsIntegration, TestWebGoogleLoginIntegration, TestWebGoogleMetadataScreenshots, TestWebGoogleSAIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and psql, none of them on this machine'],
  ['web/scripts/image-app-browser.mjs', 'browser check run by TestWebMessageImagesIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/integration/codex-auth/app-browser-verify.mjs', 'browser check run by TestWebAuthIntegration, TestWebTaskQuestionsIntegration, TestWebTasksIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and codex and gh, none of them on this machine'],
  ['web/scripts/integration/tasks/app-browser-verify.mjs', 'browser check run by TestWebAuthIntegration, TestWebTaskQuestionsIntegration, TestWebTasksIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/integration/tasks/question-app-browser-verify.mjs', 'browser check run by TestWebTaskQuestionsIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/lark-login-app.mjs', 'browser check run by TestWebLarkSwitchPublicationIntegration, TestWebLarkUserLoginFailureDeleteIntegration, TestWebLarkUserLoginIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and lark-cli, none of them on this machine'],
  ['web/scripts/lark-login-missing-cells-app.mjs', 'browser check run by TestWebLarkSwitchPublicationIntegration, TestWebLarkUserLoginFailureDeleteIntegration, TestWebLarkUserLoginIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and lark-cli, none of them on this machine'],
  ['web/scripts/lark-switch-app.mjs', 'browser check run by TestWebLarkSwitchPublicationIntegration, TestWebLarkUserLoginFailureDeleteIntegration, TestWebLarkUserLoginIntegration, TestWebTaskLarkAccountSwitchNativeIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and lark-cli, none of them on this machine'],
  ['web/scripts/project-credentials-app.mjs', 'browser check run by TestWebProjectCredentialsCRUDIntegration, TestWebProjectCredentialsDeleteIntegration, TestWebProjectCredentialsFocusIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/project-credentials-fixture.mjs', 'standalone browser fixture; needs Playwright at a fixed npx path, none of them on this machine'],
  ['web/scripts/project-login-hidden-fixture.mjs', 'standalone browser fixture; needs Playwright at a fixed npx path, none of them on this machine'],
  ['web/scripts/project-login-native-fixture.mjs', 'standalone browser fixture; needs Playwright at a fixed npx path, none of them on this machine'],
  ['web/scripts/project-login-popup-fixture.mjs', 'standalone browser fixture; needs Playwright at a fixed npx path, none of them on this machine'],
  ['web/scripts/sleep-app-browser.mjs', 'browser check run by TestWebSleepIntegration, TestWebSleepReconnectIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and gh, none of them on this machine'],
  ['web/scripts/sleep-reconnect-browser.mjs', 'browser check run by TestWebSleepIntegration, TestWebSleepReconnectIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and gh, none of them on this machine'],
  ['web/scripts/task-console-app.mjs', 'browser check run by TestWebTaskConsoleMessagesIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, none of them on this machine'],
  ['web/scripts/task-inbox-app.mjs', 'browser check run by TestWebTaskNodeInboxChatIntegration (internal/server/app); needs Playwright at a fixed npx path and Node 24.21.0 in ~/.local, and gh, none of them on this machine'],
])

// Whether a file (path from the repository root, with forward slashes) is a test file this check covers.
export function isTestFile(path) {
  if (!SCRIPT_EXTENSIONS.has(extname(path)) || path.endsWith('.gen.ts')) return false
  const parts = path.split('/')
  const browserCheck = path.startsWith('web/scripts/integration/') || (path.startsWith('web/scripts/') && /-(app|browser|fixture)\.[cm]?js$/.test(path))
  return /\.test\.[cm]?[jt]sx?$/.test(path) || parts.slice(0, -1).includes('test') || browserCheck
}

const lineOf = (file, pos) => file.getLineAndCharacterOfPosition(pos).line + 1

// The literals with Chinese in one test file that are not marked: [{ line, text }].
export function findChineseTestStrings(path, source) {
  const scriptKind = path.endsWith('.tsx') || path.endsWith('.jsx') ? ts.ScriptKind.TSX : path.endsWith('.ts') ? ts.ScriptKind.TS : ts.ScriptKind.JS
  const file = ts.createSourceFile(path, source, ts.ScriptTarget.Latest, true, scriptKind)
  const markedLines = new Set(scriptComments(path, source).filter((range) => MARKER.test(source.slice(range.pos, range.end))).map((range) => lineOf(file, range.pos)))
  const markedStatements = file.statements.filter((statement) => (ts.getLeadingCommentRanges(source, statement.pos) ?? []).some((range) => MARKER.test(source.slice(range.pos, range.end))))
  const hits = []
  const visit = (node) => {
    const literal =
      ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node) || ts.isJsxText(node) || node.kind === ts.SyntaxKind.RegularExpressionLiteral
    if (literal && CJK.test(node.text)) {
      const start = node.getStart(file)
      const marked = markedLines.has(lineOf(file, start)) || markedLines.has(lineOf(file, node.end)) || markedStatements.some((s) => start >= s.pos && node.end <= s.end)
      if (!marked) hits.push({ line: lineOf(file, start), text: node.getText(file).trim().slice(0, 120) })
    }
    ts.forEachChild(node, visit)
  }
  visit(file)
  return hits
}

// Checks the test files in every directory that exists (desktop/ and plugins/ may be missing in a partial checkout). root is the
// repository root, which the test-file rules, the supplement and the exceptions are relative to.
export function checkDirs(dirs, { root, exceptions = NOT_YET_ENGLISH } = {}) {
  const problems = []
  for (const dir of dirs.filter((d) => existsSync(d))) {
    for (const file of listFiles(dir)) {
      const path = relative(root, file).split(sep).join('/')
      if (!isTestFile(path) || path.startsWith(SUPPLEMENT) || exceptions.has(path)) continue
      for (const hit of findChineseTestStrings(path, readFileSync(file, 'utf8'))) problems.push({ file, ...hit })
    }
  }
  return problems
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const web = fileURLToPath(new URL('..', import.meta.url))
  const root = join(web, '..')
  const problems = checkDirs([web, join(root, 'desktop'), join(root, 'plugins')], { root })
  for (const p of problems) console.error(`${relative(process.cwd(), p.file)}:${p.line}: test text is not in English: ${p.text}`)
  if (problems.length > 0) {
    console.error(
      `${problems.length} string(s) in test files contain Chinese. Write tests in English against the English UI; a test of the Chinese UI goes to web/src/zh-CN, and data that must be Chinese gets a "check-english: zh (<reason>)" comment on its line.`,
    )
    // exitCode rather than exit(): on macOS, output to a pipe is asynchronous and exit() could cut the list short.
    process.exitCode = 1
  }
  const stale = [...NOT_YET_ENGLISH.keys()].filter((path) => !existsSync(join(root, path)))
  for (const path of stale) console.error(`NOT_YET_ENGLISH lists ${path}, which does not exist`)
  if (stale.length > 0) process.exitCode = 1
}
