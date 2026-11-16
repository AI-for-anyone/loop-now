// After packing, before signing: put a loopctl at a fixed location in Resources/bin. The tray item "安装命令行工具" (Install Command Line Tool…)
// uses it, so it does not need to be reinstalled after an app upgrade.
//   macOS: Resources/bin/loopctl is a relative link to ../server/loopctl-dist/<version>/loopctl-darwin-<arch>,
//         and /usr/local/bin/loopctl in turn links to this fixed location.
//   Windows: resources\bin\loopctl.exe is a copy of loopctl-windows-amd64.exe (creating links needs privileges or Developer Mode, and
//         copying 10MB is no big deal), and the tray adds resources\bin to the user PATH.
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
  if (versions.length !== 1) throw new Error(`loopctl-dist should contain exactly one version directory, found: ${versions.join(', ') || 'none'}`)
  const arch = { arm64: 'arm64', x64: 'amd64' }[Arch[context.arch]]
  const bin = join(resources, 'bin')
  mkdirSync(bin, { recursive: true })
  if (platform === 'win32') {
    const source = join(dist, versions[0], `loopctl-windows-${arch}.exe`)
    if (!existsSync(source)) throw new Error(`The release bundle has no ${source}`)
    copyFileSync(source, join(bin, 'loopctl.exe'))
    return
  }
  const target = join('..', 'server', 'loopctl-dist', versions[0], `loopctl-darwin-${arch}`)
  if (!existsSync(join(bin, target))) throw new Error(`The release bundle has no ${target}`)
  symlinkSync(target, join(bin, 'loopctl'))
}
