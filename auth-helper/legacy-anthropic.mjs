import { readFile } from 'node:fs/promises';
import { isAbsolute,join } from 'node:path';
import { pathToFileURL } from 'node:url';

const names=new Set(['ANTHROPIC_AUTH_TOKEN','ANTHROPIC_OAUTH_TOKEN','ANTHROPIC_API_KEY']);
const failed=()=>new Error('Anthropic 旧模型凭证不可用，请检查兼容配置');

// Only the platform explicitly loads this for a legacy snapshot credential.env.
// It wraps the actual installed resolver, never rewrites bearer tokens as keys
// or mutates process.env. New API-key credentials do not require this extension.
export default async function install(pi){
 let provider;
 try {
  const root=process.env.LOOP_NOW_PI_PACKAGE_ROOT;
  if(typeof root!=='string'||!isAbsolute(root))throw failed();
  const coding=JSON.parse(await readFile(join(root,'package.json'),'utf8'));
  const aiRoot=join(root,'node_modules','@earendil-works','pi-ai');
  const ai=JSON.parse(await readFile(join(aiRoot,'package.json'),'utf8'));
  if(coding.name!=='@earendil-works/pi-coding-agent'||!['1.1.0','1.4.0'].includes(coding.version)||ai.name!=='@earendil-works/pi-ai'||ai.version!==coding.version||ai.exports?.['./providers/*']?.import!=='./dist/providers/*.js')throw failed();
  const module=await import(pathToFileURL(join(aiRoot,'dist/providers/anthropic.js')).href);
  provider=module.anthropicProvider();
  if(provider.id!=='anthropic'||typeof provider.auth.apiKey.resolve!=='function')throw failed();
 }catch {throw failed();}
 const original=provider.auth.apiKey;
 pi.registerProvider({...provider,auth:{...provider.auth,apiKey:{...original,async resolve(input){
  const env=input.credential?.env;
  if(env===undefined)return original.resolve(input);
  if(!env||typeof env!=='object'||Array.isArray(env)||Object.keys(env).some(name=>!names.has(name)||typeof env[name]!=='string'))throw failed();
  const ctx={...input.ctx,env:async name=>names.has(name)?env[name]:input.ctx.env(name)};
  return original.resolve({...input,ctx});
 }}}});
}
