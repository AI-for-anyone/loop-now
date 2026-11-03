// 打包后、签名前：在 Resources/bin/loopctl 放一个指向当前版本 loopctl 的相对链接。
// 托盘"安装命令行工具"把 /usr/local/bin/loopctl 链到这个固定位置，app 升级后不用重装。
const { existsSync, mkdirSync, readdirSync, symlinkSync } = require('node:fs')
const { join } = require('node:path')
const { Arch } = require('builder-util')

exports.default = async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return
  const resources = join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, 'Contents', 'Resources')
  const dist = join(resources, 'server', 'loopctl-dist')
  const versions = readdirSync(dist).filter((name) => existsSync(join(dist, name, 'manifest.json')))
  if (versions.length !== 1) throw new Error(`loopctl-dist 里应正好有一个版本目录，实际：${versions.join(', ') || '没有'}`)
  const arch = { arm64: 'arm64', x64: 'amd64' }[Arch[context.arch]]
  const target = join('..', 'server', 'loopctl-dist', versions[0], `loopctl-darwin-${arch}`)
  const bin = join(resources, 'bin')
  if (!existsSync(join(bin, target))) throw new Error(`发行包里没有 ${target}`)
  mkdirSync(bin, { recursive: true })
  symlinkSync(target, join(bin, 'loopctl'))
}
