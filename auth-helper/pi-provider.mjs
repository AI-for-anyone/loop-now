import { readFile } from 'node:fs/promises';
import { join, isAbsolute } from 'node:path';
import { pathToFileURL } from 'node:url';

// Resolve the actual installed distribution, never the Pi source checkout or a
// coding-agent session. No AuthStorage, extensions, Skills or HOME are loaded.
export async function loadPiProvider(root) {
 if (typeof root !== 'string' || !isAbsolute(root)) throw new Error('pi_incompatible');
 const expected = process.env.LOOP_NOW_AUTH_PI_VERSION ?? '1.4.0';
 if (!['1.1.0','1.4.0'].includes(expected)) throw new Error('pi_incompatible');
 const meta = JSON.parse(await readFile(join(root, 'package.json'), 'utf8'));
 if (meta.name !== '@earendil-works/pi-coding-agent' || meta.version !== expected) throw new Error('pi_incompatible');
 const aiRoot = join(root, 'node_modules', '@earendil-works', 'pi-ai');
 const entry = join(aiRoot, 'dist/providers/openai-codex.js');
 const aiMeta = JSON.parse(await readFile(join(aiRoot, 'package.json'), 'utf8'));
 if (aiMeta.name !== '@earendil-works/pi-ai' || aiMeta.version !== expected || aiMeta.exports?.['./providers/*']?.import !== './dist/providers/*.js') throw new Error('pi_incompatible');
 const { openaiCodexProvider } = await import(pathToFileURL(entry).href);
 const provider = openaiCodexProvider();
 if (provider.id !== 'openai-codex' || typeof provider.auth.oauth.login !== 'function' || typeof provider.auth.oauth.refresh !== 'function') throw new Error('pi_incompatible');
 return { oauth: provider.auth.oauth, models: provider.getModels(), entry, aiRoot, version: aiMeta.version };
}

export function credential(value) {
 if (!value || value.type !== 'oauth' || typeof value.access !== 'string' || !value.access || typeof value.refresh !== 'string' || !value.refresh || !Number.isFinite(value.expires) || value.expires<=0 || value.expires>Number.MAX_SAFE_INTEGER || typeof value.accountId !== 'string' || !value.accountId) throw new Error('invalid_credential');
 const expires=Math.floor(value.expires);
 if(!Number.isSafeInteger(expires)||expires<=0)throw new Error('invalid_credential');
 const out={ type: 'oauth', access: value.access, refresh: value.refresh, expires, accountId: value.accountId };
 if(Buffer.byteLength(JSON.stringify(out))>64*1024)throw new Error('invalid_credential');
 return out;
}
