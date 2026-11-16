#!/usr/bin/env node
// loop-now-plugin validate <dir> [--platform-version YYYY.MM]: validates a plugin directory against plugins/SPEC.md §2 and writes JSON to stdout.
// Exit code 1 when there are errors, 2 for bad arguments. Static checks only; to load the platform side and check its subscriptions,
// use the plugin host's validate --load.

import { validatePlugin } from './validate.ts'

function main(argv: string[]): number {
  const [command, ...rest] = argv
  if (command !== 'validate') {
    process.stderr.write('Usage: loop-now-plugin validate <plugin dir> [--platform-version YYYY.MM]\n')
    return 2
  }
  let dir: string | undefined
  let platformVersion: string | undefined
  for (let i = 0; i < rest.length; i++) {
    const arg = rest[i]!
    if (arg === '--platform-version') platformVersion = rest[++i]
    else if (dir === undefined) dir = arg
    else {
      process.stderr.write(`Unexpected argument: ${arg}\n`)
      return 2
    }
  }
  if (!dir) {
    process.stderr.write('Missing plugin directory\n')
    return 2
  }
  const result = validatePlugin(dir, { platformVersion })
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`)
  return result.ok ? 0 : 1
}

process.exitCode = main(process.argv.slice(2))
