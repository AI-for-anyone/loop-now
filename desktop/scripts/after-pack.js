// 打包后、签名前：在 Resources/bin 放一个固定位置的 loopctl，托盘"安装命令行工具"用它，app 升级后不用重装。
//   macOS：Resources/bin/loopctl 是指向 ../server/loopctl-dist/<版本>/loopctl-darwin-<arch> 的相对链接，
//         /usr/local/bin/loopctl 再链到这个固定位置。
//   Windows：resources\bin\loopctl.exe 是 loopctl-windows-amd64.exe 的副本（建链接要特权或开发者模式，复制一份 10MB 不算什么），
//         托盘把 resources\bin 加进用户 PATH。
const { copyFileSync, existsSync, mkdirSync, readdirSync, symlinkSync } = require('node:fs')
const { join } = require('node:path')
const { Arch } = require('builder-util')

exports.default = async function afterPack(context) {
  const platform = context.electronPlatformName
  if (platform !== 'darwin' && platform !== 'win32') return
  const resources =
    platform === 'darwin' ? join(context.appOutDir, `${context.packager.appInfo.productFilename}.app`, 'Contents', 'Resources') : join(context.appOutDir, 'resources')
  const dist = join(resources, 'server', 'loopctl-dist')
  const versions = readdirSync(dist).filter((name) => existsSync(join(dist, name, 'manifest.json')))
  if (versions.length !== 1) throw new Error(`loopctl-dist 里应正好有一个版本目录，实际：${versions.join(', ') || '没有'}`)
  const arch = { arm64: 'arm64', x64: 'amd64' }[Arch[context.arch]]
  const bin = join(resources, 'bin')
  mkdirSync(bin, { recursive: true })
  if (platform === 'win32') {
    const source = join(dist, versions[0], `loopctl-windows-${arch}.exe`)
    if (!existsSync(source)) throw new Error(`发行包里没有 ${source}`)
    copyFileSync(source, join(bin, 'loopctl.exe'))
    return
  }
  const target = join('..', 'server', 'loopctl-dist', versions[0], `loopctl-darwin-${arch}`)
  if (!existsSync(join(bin, target))) throw new Error(`发行包里没有 ${target}`)
  symlinkSync(target, join(bin, 'loopctl'))
}
