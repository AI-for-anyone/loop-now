// Plugin host entry (plugins/SPEC.md §3). Go starts it with `node main.js`; `node main.js validate <dir>` validates a plugin directory.

import { Host, HOST_VERSION } from './host.ts'
import { runValidate } from './validate.ts'

const [command, ...args] = process.argv.slice(2)
if (command === 'validate') {
  process.exitCode = await runValidate(args)
} else if (command === '--version') {
  process.stdout.write(`${HOST_VERSION}\n`)
} else if (command === undefined) {
  new Host().start()
} else {
  process.stderr.write(`Unknown argument ${command}; run the host without arguments, or validate <plugin dir>\n`)
  process.exitCode = 2
}
