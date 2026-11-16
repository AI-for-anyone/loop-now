// Confirm that the current Node and npm versions match .node-version and the engines field of package.json.
import { readFileSync } from 'node:fs'

const webDir = new URL('../', import.meta.url)
const expectedNode = readFileSync(new URL('.node-version', webDir), 'utf8').trim()
const pkg = JSON.parse(readFileSync(new URL('package.json', webDir), 'utf8'))

const problems = []
if (pkg.engines?.node !== expectedNode) {
  problems.push(`engines.node in package.json is ${pkg.engines?.node}, which does not match ${expectedNode} in .node-version`)
}
if (process.version !== `v${expectedNode}`) {
  problems.push(`Node version is ${process.version}, expected v${expectedNode}`)
}
const npmVersion = /\bnpm\/(\S+)/.exec(process.env.npm_config_user_agent ?? '')?.[1]
if (npmVersion !== undefined && npmVersion !== pkg.engines?.npm) {
  problems.push(`npm version is ${npmVersion}, expected ${pkg.engines?.npm}`)
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`check-node: ${problem}`)
  console.error('check-node: run commands through ./bin/npm24.')
  process.exit(1)
}
