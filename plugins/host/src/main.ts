// 插件宿主入口（plugins/SPEC.md §3）。Go 以 `node main.js` 启动它；`node main.js validate <目录>` 校验插件目录。

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
  process.stderr.write(`未知的参数 ${command}；不带参数运行宿主，或 validate <插件目录>\n`)
  process.exitCode = 2
}
