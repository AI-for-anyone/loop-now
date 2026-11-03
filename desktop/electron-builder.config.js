// 打包 macOS arm64：DMG 与更新用的 zip，ad-hoc 签名、不公证。
// 服务端发行包的平台目录（scripts/release/build.sh 的产物）由 LOOP_NOW_DESKTOP_BUNDLE 指定，整个放进 Resources/server。
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
  // 壳只用 Electron 跑自己的主进程；服务端、Pi、插件宿主用随包的官方 Node，不需要 Electron 当 Node 用。
  electronFuses: {
    runAsNode: false,
    enableNodeOptionsEnvironmentVariable: false,
    enableNodeCliInspectArguments: false,
    onlyLoadAppFromAsar: true,
    resetAdHocDarwinSignature: true,
  },
  // 在 Resources/bin/loopctl 放指向当前版本 loopctl 的固定链接（托盘"安装命令行工具"用）。
  afterPack: './scripts/after-pack.js',
  publish: null,
}
