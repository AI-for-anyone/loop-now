#!/usr/bin/env node
// Read installed package metadata and public provider exports. Never read user auth files.
import {access, readFile, realpath} from 'node:fs/promises';
import {dirname, join, resolve, delimiter} from 'node:path';
import {pathToFileURL} from 'node:url';
import {constants} from 'node:fs';
import {createHash} from 'node:crypto';

async function locatePi(value) {
  if (value.includes('/') || value.includes('\\')) return realpath(resolve(value));
  for (const dir of (process.env.PATH ?? '').split(delimiter)) {
    if (!dir) continue;
    const candidate = join(dir, value);
    try { await access(candidate, constants.X_OK); return realpath(candidate); } catch {}
  }
  throw new Error('pi executable not found; provide --pi-bin PATH');
}
async function packageRoot(entry) {
  let dir = dirname(entry);
  for (let depth = 0; depth < 10; depth++) {
    try {
      const pkg = JSON.parse(await readFile(join(dir, 'package.json'), 'utf8'));
      if (pkg.name === '@earendil-works/pi-coding-agent') return {dir, pkg};
    } catch {}
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error('pi entry is not inside the expected coding-agent package');
}
async function main() {
  const args = process.argv.slice(2);
  if (args.length && (args.length !== 2 || args[0] !== '--pi-bin')) {
    throw new Error('usage: node scripts/dev/check-pi-auth-capabilities.mjs [--pi-bin PATH]');
  }
  const entry = await locatePi(args[1] ?? process.env.LOOP_NOW_PI_BIN ?? 'pi');
  const {dir, pkg} = await packageRoot(entry);
  if (pkg.version !== '1.1.0') throw new Error('expected installed Pi version 1.1.0');
  const aiRoot = join(dir, 'node_modules', '@earendil-works', 'pi-ai');
  const aiPkg = JSON.parse(await readFile(join(aiRoot, 'package.json'), 'utf8'));
  if (aiPkg.name !== '@earendil-works/pi-ai' || aiPkg.version !== '1.1.0') {
    throw new Error('expected installed pi-ai version 1.1.0');
  }
  const providerModule = await import(pathToFileURL(join(aiRoot, 'dist/providers/openai-codex.js')).href);
  const provider = providerModule.openaiCodexProvider();
  const oauth = provider.auth?.oauth;
  if (provider.id !== 'openai-codex' || !oauth || ['login', 'refresh', 'toAuth'].some(k => typeof oauth[k] !== 'function')) {
    throw new Error('installed Codex provider does not expose the required OAuth capabilities');
  }
  const oauthModule = await import(pathToFileURL(join(aiRoot, 'dist/oauth.js')).href);
  const distributionSha256 = {};
  for (const [name, file] of Object.entries({provider: join(aiRoot, 'dist/providers/openai-codex.js'), oauth: join(aiRoot, 'dist/auth/oauth/openai-codex.js'), callback: join(aiRoot, 'dist/auth/oauth/callback-server.js'), storage: join(dir, 'dist/core/auth-storage.js')})) {
    distributionSha256[name] = createHash('sha256').update(await readFile(file)).digest('hex');
  }
  console.log(JSON.stringify({
    ok: true, evidence: 'installed-package-metadata-and-public-exports',
    pi: {version: pkg.version, entry, packageRoot: dir},
    piAI: {version: aiPkg.version},
    provider: {id: provider.id, name: provider.name, modelCount: provider.getModels().length,
      oauth: {login: true, refresh: true, toAuth: true}},
    oauthModuleExportNames: Object.keys(oauthModule).sort(),
    distributionSha256,
    verified: ['package versions', 'provider factory', 'OAuth method availability'],
    notVerified: ['browser login', 'callback port binding', 'runtime extension wiring', 'concurrent refresh', 'model requests'],
    userCredentialsRead: false,
  }, null, 2));
}
main().catch(() => {
  // Do not echo imported errors: third-party errors may contain environment values or credentials.
  console.error(JSON.stringify({ok: false, error: 'Pi authentication capability check failed; verify the executable path, package version and public exports.'}));
  process.exitCode = 1;
});
