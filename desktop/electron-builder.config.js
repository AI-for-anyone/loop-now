// Packages macOS arm64 (a DMG plus the zip used for updates; ad-hoc signed, not notarized) and Windows x64 (an NSIS installer, unsigned).
// LOOP_NOW_DESKTOP_BUNDLE points at the platform directory of the server release bundle (the output of scripts/release/build.sh; on Windows
// the set built around loop-server.exe), and the whole directory goes into Resources/server (resources\server on Windows).
// JS rather than YAML: in YAML, ${env.…} is expanded only after paths are resolved, so an absolute path would get appended to the project directory.
const bundle = process.env.LOOP_NOW_DESKTOP_BUNDLE
if (!bundle) throw new Error('LOOP_NOW_DESKTOP_BUNDLE is not set: point it at the platform directory of the server release bundle')

/** @type {import('electron-builder').Configuration} */
module.exports = {
  appId: 'io.github.ai-for-anyone.loop-now',
  productName: 'Loop Now',
  copyright: 'Copyright © 2026 Loop Now contributors',
  directories: { output: '../.local-build/desktop', buildResources: 'build' },
  files: ['package.json', 'dist/**/*.js', '!dist/**/*.test.js', 'static/**', 'assets/**'],
  extraResources: [{ from: bundle, to: 'server' }],
  // macOS: loop-now-desktop-<version>-mac-arm64.dmg/.zip; Windows: loop-now-desktop-<version>-win-x64.exe.
  artifactName: 'loop-now-desktop-${version}-${os}-${arch}.${ext}',
  mac: {
    target: [
      { target: 'dmg', arch: 'arm64' },
      { target: 'zip', arch: 'arm64' },
    ],
    category: 'public.app-category.developer-tools',
    icon: 'build/icon.icns',
    minimumSystemVersion: '12.0',
    // Ad-hoc signing until a certificate is in place. Under ad-hoc signing, the hardened runtime would refuse to load the frameworks Electron
    // ships with their own signatures.
    identity: '-',
    // The binaries in loopctl-dist are payloads the server checks against its manifest (size, SHA-256) before handing them to Agents. Signing
    // would change their contents and the server would then refuse to start; they are already signed by the Go linker. Resources/bin/loopctl
    // is a link to one of them, and signing it would follow the link and modify the original file.
    signIgnore: ['/Contents/Resources/server/loopctl-dist/', '/Contents/Resources/bin/'],
    hardenedRuntime: false,
    gatekeeperAssess: false,
    notarize: false,
  },
  dmg: { title: 'Loop Now' },
  // Windows x64: NSIS installer. Unsigned (no certificate): SmartScreen shows "Windows protected your PC" and the user has to click
  // "More info → Run anyway"; that is the only way until signing is in place. The signtool settings (certificateFile etc.) go here then.
  win: {
    target: [{ target: 'nsis', arch: 'x64' }],
    icon: 'build/icon.ico',
    // Without a certificate, electron-builder skips signing. publisherName is not set: electron-updater verifies the update package's
    // signature only when it is set (and it only notifies anyway, it never installs automatically; see src/updates.ts).
  },
  nsis: {
    // Wizard-style install (not one-click), installed for the current user (%LOCALAPPDATA%\Programs\Loop Now, no administrator needed),
    // and the install directory can be changed.
    oneClick: false,
    perMachine: false,
    allowToChangeInstallationDirectory: true,
    allowElevation: false,
    // Run right after installing. Uninstalling deletes no data: neither the shell's own data (%APPDATA%\Loop Now) nor the profile
    // directories (%LOCALAPPDATA%\Loop Now).
    runAfterFinish: true,
    deleteAppDataOnUninstall: false,
    shortcutName: 'Loop Now',
    uninstallDisplayName: 'Loop Now',
    installerIcon: 'build/icon.ico',
    uninstallerIcon: 'build/icon.ico',
    // Upgrading does not require quitting the old version first: the installer itself ends the running app (electron-builder's default
    // behavior), and the shell stops the server.
    artifactName: 'loop-now-desktop-${version}-${os}-${arch}.${ext}',
  },
  // The shell uses Electron only to run its own main process; the server, Pi and the plugin host use the bundled official Node, so Electron
  // is never needed as Node.
  electronFuses: {
    runAsNode: false,
    enableNodeOptionsEnvironmentVariable: false,
    enableNodeCliInspectArguments: false,
    onlyLoadAppFromAsar: true,
    resetAdHocDarwinSignature: true,
  },
  // macOS gets a fixed link at Resources/bin/loopctl pointing to the current version's loopctl; Windows gets loopctl-windows-amd64.exe copied
  // to resources\bin\loopctl.exe (used by the tray item "安装命令行工具" (Install Command Line Tool…): macOS links /usr/local/bin/loopctl to
  // the former, Windows adds the latter's directory to the user PATH).
  afterPack: './scripts/after-pack.js',
  // electron-updater reads Resources/app-update.yml to find updates: GitHub Releases, stable releases and pre-releases alike (for the channel
  // trade-offs see src/updates.ts).
  // With no channel set, the update info files are latest-mac.yml (macOS) and latest.yml (Windows), in pre-releases too:
  // electron-updater looks for rc-mac.yml/rc.yml first and falls back to the latest ones.
  // This only packages and does not upload (--publish never); the Release is created by release.yml, and the tag decides whether it is a pre-release.
  publish: { provider: 'github', owner: 'AI-for-anyone', repo: 'loop-now' },
}
