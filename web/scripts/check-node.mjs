// 确认当前的 Node 与 npm 版本与 .node-version、package.json 的 engines 一致。
import { readFileSync } from 'node:fs'

const webDir = new URL('../', import.meta.url)
const expectedNode = readFileSync(new URL('.node-version', webDir), 'utf8').trim()
const pkg = JSON.parse(readFileSync(new URL('package.json', webDir), 'utf8'))

const problems = []
if (pkg.engines?.node !== expectedNode) {
  problems.push(`package.json 的 engines.node 为 ${pkg.engines?.node}，与 .node-version 的 ${expectedNode} 不一致`)
}
if (process.version !== `v${expectedNode}`) {
  problems.push(`Node 版本为 ${process.version}，期望 v${expectedNode}`)
}
const npmVersion = /\bnpm\/(\S+)/.exec(process.env.npm_config_user_agent ?? '')?.[1]
if (npmVersion !== undefined && npmVersion !== pkg.engines?.npm) {
  problems.push(`npm 版本为 ${npmVersion}，期望 ${pkg.engines?.npm}`)
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`check-node: ${problem}`)
  console.error('check-node: 请通过 ./bin/npm24 运行命令。')
  process.exit(1)
}
