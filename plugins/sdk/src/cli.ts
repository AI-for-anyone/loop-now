#!/usr/bin/env node
// loop-now-plugin validate <目录> [--platform-version YYYY.MM]：按 plugins/SPEC.md §2 校验插件目录，向标准输出写 JSON。
// 有错误时退出码为 1，参数不对时为 2。只做静态检查；加载平台半边核对订阅用插件宿主的 validate --load。

import { validatePlugin } from './validate.ts'

function main(argv: string[]): number {
  const [command, ...rest] = argv
  if (command !== 'validate') {
    process.stderr.write('用法：loop-now-plugin validate <插件目录> [--platform-version YYYY.MM]\n')
    return 2
  }
  let dir: string | undefined
  let platformVersion: string | undefined
  for (let i = 0; i < rest.length; i++) {
    const arg = rest[i]!
    if (arg === '--platform-version') platformVersion = rest[++i]
    else if (dir === undefined) dir = arg
    else {
      process.stderr.write(`多余的参数：${arg}\n`)
      return 2
    }
  }
  if (!dir) {
    process.stderr.write('缺少插件目录\n')
    return 2
  }
  const result = validatePlugin(dir, { platformVersion })
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`)
  return result.ok ? 0 : 1
}

process.exitCode = main(process.argv.slice(2))
