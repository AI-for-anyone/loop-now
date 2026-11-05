// 打包 macOS arm64（DMG 与更新用的 zip，ad-hoc 签名、不公证）和 Windows x64（NSIS 安装包，不签名）。
// 服务端发行包的平台目录（scripts/release/build.sh 的产物；Windows 是 loop-server.exe 那一套）由 LOOP_NOW_DESKTOP_BUNDLE 指定，
// 整个放进 Resources/server（Windows 是 resources\server）。
// 用 JS 而不是 YAML：YAML 里的 ${env.…} 在解析路径之后才展开，绝对路径会被拼到项目目录后面。
const bundle = process.env.LOOP_NOW_DESKTOP_BUNDLE
if (!bundle) throw new Error('LOOP_NOW_DESKTOP_BUNDLE 未设置：指向服务端发行包的平台目录')

/** @type {import('electron-builder').Configuration} */
module.exports = {
  appId: 'io.github.ai-for-anyone.loop-now',
  productName: 'Loop Now',
  copyright: 'Copyright © 2026 Loop Now contributors',
  directories: { output: '../.local-build/desktop', buildResources: 'build' },
  files: ['package.json', 'dist/**/*.js', '!dist/**/*.test.js', 'static/**', 'assets/**'],
  extraResources: [{ from: bundle, to: 'server' }],
  // macOS：loop-now-desktop-<版本>-mac-arm64.dmg／.zip；Windows：loop-now-desktop-<版本>-win-x64.exe。
  artifactName: 'loop-now-desktop-${version}-${os}-${arch}.${ext}',
  mac: {
    target: [
      { target: 'dmg', arch: 'arm64' },
      { target: 'zip', arch: 'arm64' },
    ],
    category: 'public.app-category.developer-tools',
    icon: 'build/icon.icns',
    minimumSystemVersion: '12.0',
    // 证书到位前用 ad-hoc 签名；ad-hoc 下开 hardened runtime 会拒绝加载 Electron 自带签名的框架。
    identity: '-',
    // loopctl-dist 里的二进制是服务端按清单（大小、SHA-256）核对后发给 Agent 的载荷，签名会改动它们的内容，
    // 服务端就会拒绝启动；它们自己是 Go 链接时签好的。Resources/bin/loopctl 是指向其中一个的链接，签它会顺着链接改到原文件。
    signIgnore: ['/Contents/Resources/server/loopctl-dist/', '/Contents/Resources/bin/'],
    hardenedRuntime: false,
    gatekeeperAssess: false,
    notarize: false,
  },
  dmg: { title: 'Loop Now' },
  // Windows x64：NSIS 安装包。不签名（没有证书）：SmartScreen 会提示"Windows 已保护你的电脑"，要点"更多信息 → 仍要运行"；
  // 签名到位前只能这样。signtool 的配置（certificateFile 等）到时加在这里。
  win: {
    target: [{ target: 'nsis', arch: 'x64' }],
    icon: 'build/icon.ico',
    // 没有证书时 electron-builder 跳过签名。不设 publisherName：electron-updater 只在设了它时核对更新包的签名
    // （它本来也只提示不自动装，见 src/updates.ts）。
  },
  nsis: {
    // 向导式安装（不是一键）、装给当前用户（%LOCALAPPDATA%\Programs\Loop Now，不需要管理员）、允许改安装目录。
    oneClick: false,
    perMachine: false,
    allowToChangeInstallationDirectory: true,
    allowElevation: false,
    // 安装完直接运行；卸载时删掉壳自己的数据（%APPDATA%\Loop Now）但不动配置目录（%LOCALAPPDATA%\Loop Now）。
    runAfterFinish: true,
    deleteAppDataOnUninstall: false,
    shortcutName: 'Loop Now',
    uninstallDisplayName: 'Loop Now',
    installerIcon: 'build/icon.ico',
    uninstallerIcon: 'build/icon.ico',
    // 升级时不用关掉旧版本再装：安装器自己会结束正在运行的 app（electron-builder 的默认行为），服务端由壳停掉。
    artifactName: 'loop-now-desktop-${version}-${os}-${arch}.${ext}',
  },
  // 壳只用 Electron 跑自己的主进程；服务端、Pi、插件宿主用随包的官方 Node，不需要 Electron 当 Node 用。
  electronFuses: {
    runAsNode: false,
    enableNodeOptionsEnvironmentVariable: false,
    enableNodeCliInspectArguments: false,
    onlyLoadAppFromAsar: true,
    resetAdHocDarwinSignature: true,
  },
  // macOS 在 Resources/bin/loopctl 放指向当前版本 loopctl 的固定链接；Windows 把 loopctl-windows-amd64.exe 复制成 resources\bin\loopctl.exe
  // （托盘"安装命令行工具"用：macOS 把 /usr/local/bin/loopctl 链到前者，Windows 把后者所在目录加进用户 PATH）。
  afterPack: './scripts/after-pack.js',
  // electron-updater 读 Resources/app-update.yml 找更新：GitHub Releases，正式版与预发布都看（通道的取舍见 src/updates.ts）。
  // 不设 channel，更新信息文件就是 latest-mac.yml（macOS）与 latest.yml（Windows），预发布的 release 里也是它们：
  // electron-updater 先找 rc-mac.yml／rc.yml，没有就读 latest 的。
  // 这里只打包，不上传（--publish never）；Release 由 release.yml 创建，是否预发布按 tag 定。
  publish: { provider: 'github', owner: 'AI-for-anyone', repo: 'loop-now' },
}
