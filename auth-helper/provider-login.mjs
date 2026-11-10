// 插件提供方的订阅登录（plugins/SPEC.md §13）：一进程一操作。用随包 Pi 自己的扩展加载器加载插件的 Pi 扩展，
// 找到注册了 provider 的 OAuth 登录并调用它；HOME 是这次登录的新状态目录，扩展把自己的状态写在这里。
// 与 Codex 登录同样的帧协议（fd3 入、fd4 出，见 protocol.md）；扩展给的文字一律按不可信纯文本处理（限长），
// 进度不转扩展原文；login 的返回值只在 result 帧里经私有管道交给服务端，不进日志。
import http from 'node:http';
import { syncBuiltinESMExports } from 'node:module';
import { isAbsolute, join } from 'node:path';
import { pathToFileURL } from 'node:url';

const MAX_MESSAGE = 500, MAX_LABEL = 200, MAX_OPTIONS = 20, MAX_REPLY = 4096, MAX_ENTRY = 64 * 1024;

// 去掉控制字符（保留换行），截断到 max 个字符。
export function plainText(value, max) {
 const text = String(value ?? '').replace(/[\u0000-\u0009\u000b-\u001f\u007f-\u009f]/g, ' ');
 const chars = [...text];
 return chars.length > max ? chars.slice(0, max).join('') + '…' : text;
}

// 授权地址与设备码地址：https、没有用户名密码与 fragment、查询里不带授权码或令牌。
export function safeURL(raw) {
 let url;
 try { url = new URL(String(raw)); } catch { return null; }
 if (url.protocol !== 'https:' || url.username || url.password || url.hash || String(raw).length > 4096) return null;
 for (const key of url.searchParams.keys()) {
  if (['code', 'access_token', 'refresh_token', 'id_token', 'token', 'client_secret', 'password'].includes(key.toLowerCase())) return null;
 }
 return url.href;
}

// 观察登录期间扩展起的本地监听：端口被占用时记下（扩展自己可能换端口继续），登录失败时据此报 auth_port_busy。
function watchListeners(onBusy) {
 const original = http.createServer;
 http.createServer = function (...args) {
  const server = original.apply(this, args);
  server.on('error', error => { if (error?.code === 'EADDRINUSE') onBusy(); });
  return server;
 };
 syncBuiltinESMExports();
 return () => { http.createServer = original; syncBuiltinESMExports(); };
}

export async function loadProvider(root, extensions, provider) {
 if (typeof root !== 'string' || !isAbsolute(root) || !Array.isArray(extensions) || extensions.length === 0 || extensions.some(p => typeof p !== 'string' || !isAbsolute(p))) throw new Error('extension_load_failed');
 const { loadExtensions } = await import(pathToFileURL(join(root, 'dist', 'core', 'extensions', 'index.js')).href);
 const loaded = await loadExtensions(extensions, process.env.HOME);
 const found = loaded.runtime.pendingProviderRegistrations.filter(r => r.name === provider).at(-1);
 const oauth = found?.config?.oauth;
 if (!oauth || typeof oauth.login !== 'function') throw new Error('extension_load_failed');
 return oauth;
}

export function createProviderLogin({ send, load = loadProvider }) {
 let operation, ended = false, promptNumber = 0, timer, restore = () => {}, portBusy = false;
 const prompts = new Map();
 const finish = (status, extra = {}) => {
  if (ended) return;
  ended = true; clearTimeout(timer); restore();
  for (const [id, p] of prompts) { p.reject(new Error('cancelled')); send({ type: 'prompt_closed', id: operation?.id, promptId: id }); }
  prompts.clear();
  send({ type: 'result', id: operation?.id ?? null, status, ...extra });
 };
 const abort = status => { if (ended) return; finish(status); operation?.controller.abort(new Error(status)); };
 const fail = error => {
  const code = error?.message === 'extension_load_failed' || error?.message === 'invalid_authorization_url' || error?.message === 'invalid_credential' ? error.message : portBusy ? 'auth_port_busy' : 'auth_failed';
  finish('failed', { code });
 };
 // 扩展给出不合规的地址时立即结束这次登录（扩展可能吞掉异常继续等回调）。
 const refuse = code => { const error = new Error(code); fail(error); operation.controller.abort(); throw error; };
 const ask = (kind, message, options) => {
  if (ended || operation.controller.signal.aborted) return Promise.reject(new Error('cancelled'));
  const promptId = String(++promptNumber);
  return new Promise((resolve, reject) => {
   prompts.set(promptId, { kind, options, resolve, reject });
   send({ type: 'prompt', id: operation.id, promptId, kind, message, ...(options ? { options } : {}) });
  });
 };
 const callbacks = () => ({
  signal: operation.controller.signal,
  onAuth(info) {
   const url = safeURL(info?.url);
   if (!url) refuse('invalid_authorization_url');
   send({ type: 'notify', id: operation.id, event: { type: 'auth_url', url } });
  },
  onDeviceCode(info) {
   const uri = safeURL(info?.verificationUri);
   const code = plainText(info?.userCode, 64).trim();
   if (!uri || !code) refuse('invalid_authorization_url');
   const expires = Number.isFinite(info?.expiresInSeconds) && info.expiresInSeconds > 0 ? Math.floor(info.expiresInSeconds) : undefined;
   send({ type: 'notify', id: operation.id, event: { type: 'device_code', userCode: code, verificationUri: uri, ...(expires ? { expiresInSeconds: expires } : {}) } });
  },
  onProgress() { if (!ended) send({ type: 'notify', id: operation.id, event: { type: 'progress' } }); },
  onPrompt(prompt) { return ask('text', plainText(prompt?.message, MAX_MESSAGE)); },
  onManualCodeInput() { return ask('manual_code', ''); },
  async onSelect(prompt) {
   const options = (Array.isArray(prompt?.options) ? prompt.options : []).slice(0, MAX_OPTIONS)
    .map(o => ({ id: plainText(o?.id, MAX_LABEL), label: plainText(o?.label ?? o?.id, MAX_LABEL) })).filter(o => o.id);
   if (options.length === 0) throw new Error('auth_failed');
   return ask('select', plainText(prompt?.message, MAX_MESSAGE), options);
  },
 });
 const receive = record => {
  if (ended) return;
  if (!record || typeof record !== 'object' || Array.isArray(record)) { abort('protocol_error'); return; }
  if (record.type === 'abort') { if (operation && record.id === operation.id) abort('cancelled'); return; }
  if (record.type === 'prompt_reply') {
   if (!operation || record.id !== operation.id) return;
   const pending = prompts.get(record.promptId);
   if (!pending || typeof record.value !== 'string' || record.value.length > MAX_REPLY) return;
   if (pending.kind === 'select' && !pending.options.some(o => o.id === record.value)) return;
   prompts.delete(record.promptId);
   pending.resolve(record.value);
   return;
  }
  if (operation || record.type !== 'login' || typeof record.provider !== 'string' || !record.provider || typeof record.id !== 'string' || !/^[A-Za-z0-9_-]{1,100}$/.test(record.id) || !Number.isSafeInteger(record.timeoutMs) || record.timeoutMs < 1 || record.timeoutMs > 2147483647) { abort('protocol_error'); return; }
  operation = { id: record.id, controller: new AbortController() };
  timer = setTimeout(() => abort('expired'), record.timeoutMs);
  void (async () => {
   try {
    const oauth = await load(record.piPackageRoot, record.extensions, record.provider);
    if (ended) return;
    restore = watchListeners(() => { portBusy = true; });
    const credentials = await oauth.login(callbacks());
    if (ended) return;
    if (!credentials || typeof credentials !== 'object' || Array.isArray(credentials)) throw new Error('invalid_credential');
    const entry = { ...credentials, type: 'oauth' };
    if (Buffer.byteLength(JSON.stringify(entry)) > MAX_ENTRY) throw new Error('invalid_credential');
    finish('succeeded', { authEntry: entry });
   } catch (error) { fail(error); operation.controller.abort(); }
  })();
 };
 return { receive, inputStopped: () => ended, close: () => abort('cancelled'), protocolError: () => abort('protocol_error') };
}
