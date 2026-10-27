import { credential, loadPiProvider } from './pi-provider.mjs';
import { guardCallbacks } from './callback-guard.mjs';
import { createServer } from 'node:net';
import { withCodexOAuthScope } from './pi-oauth-scope.mjs';

const MAX_RECORD = 1024 * 1024;
export { MAX_RECORD };
const safeCodes = new Set(['pi_incompatible','callback_port_in_use','invalid_credential','account_mismatch','invalid_prompt_reply']);
function portAvailable(port) {
 return new Promise((resolve, reject) => {
  const server = createServer();
  server.once('error', error => reject(new Error(error.code === 'EADDRINUSE' ? 'callback_port_in_use' : 'auth_failed')));
  server.listen(port, '127.0.0.1', () => server.close(error => error ? reject(new Error('auth_failed')) : resolve()));
 });
}

// One operation per child. send is a bounded private IPC writer, never a logger.
export function createHelper({ send, loadProvider = loadPiProvider, callbackPort = 1455 }) {
 let operation, ended = false, promptNumber = 0, authState;
 const prompts = new Map();
 let timer, restore = () => {};
 let lastDiagnostic;
 const diagnostic = value => {
  if(ended || operation?.kind!=='login')return;
  lastDiagnostic=value;
  send({type:'diagnostic',id:operation.id,...value});
 };
 const finish = (status, extra = {}) => {
  if (ended) return;
  const failureDiagnostic=operation?.kind==='login'&&status==='failed'?{stage:lastDiagnostic?.stage??'provider_loaded',category:lastDiagnostic?.category??'unknown_error',...(lastDiagnostic?.httpStatus?{httpStatus:lastDiagnostic.httpStatus}:{})}:undefined;
  if(operation?.kind==='login')diagnostic({stage:'result_queued',...(lastDiagnostic?.httpStatus?{httpStatus:lastDiagnostic.httpStatus}:{}),...(status==='failed'?{category:lastDiagnostic?.category??'unknown_error'}:{})});
  ended = true; clearTimeout(timer);
  for (const [id,p] of prompts) { p.cleanup(); p.reject(new Error('cancelled')); send({type:'prompt_closed',id:operation?.id,promptId:id}); }
  prompts.clear();
  restore();
  send({type:'result', id:operation?.id ?? null, status, ...extra,...(failureDiagnostic?{diagnostic:failureDiagnostic}:{})});
 };
 const abort = code => {
  if (ended) return;
  // Ordinary waiter cancellation cannot discard an already rotating grant.
  // The server must durably commit the secret result before serving cancellation.
  if (operation?.kind==='refresh' && operation.started && code!=='expired') { operation.cancelRequested=true; if(code==='protocol_error')operation.protocolFault=true; return; }
  if (operation?.kind==='refresh' && operation.started && code==='expired') finish('failed',{code:'refresh_uncertain'});
  else finish(code);
  operation?.controller.abort(new Error(code));
 };
 const fail = error => { if (!ended) finish('failed',{code:safeCodes.has(error?.message) ? error.message : operation?.kind==='refresh'&&operation.started ? 'refresh_uncertain' : 'auth_failed'}); };
 const notify = event => {
  if (ended) return;
  if (event.type === 'auth_url') {
   const url = new URL(event.url);
   const allowed=new Set(['response_type','client_id','redirect_uri','scope','code_challenge','code_challenge_method','state','id_token_add_organizations','codex_cli_simplified_flow','originator']);
   if(url.username||url.password||[...url.searchParams.keys()].some(k=>!allowed.has(k)))throw new Error('auth_failed');
   if (url.origin !== 'https://auth.openai.com' || url.pathname !== '/oauth/authorize' || !url.searchParams.get('state') || url.searchParams.has('code') || url.searchParams.has('access_token') || url.searchParams.has('refresh_token')) throw new Error('auth_failed');
   authState = url.searchParams.get('state');
   send({type:'notify',id:operation.id,event:{type:'auth_url',url:url.href}});
  } else if (event.type === 'progress' || event.type === 'info') {
   // Provider text is untrusted and may contain token-response error bodies.
   send({type:'notify',id:operation.id,event:{type:'progress',message:'等待 Codex 授权'}});
  } else throw new Error('auth_failed'); // device flow is not silently enabled.
 };
 const prompt = spec => {
  if (ended || operation.controller.signal.aborted || spec.signal?.aborted) return Promise.reject(new Error('cancelled'));
  // Browser-only product flow; do not expose the provider's headless alternative.
  if (spec.type === 'select' && spec.options?.some(o=>o.id==='browser')) return Promise.resolve('browser');
  if (spec.type !== 'manual_code') return Promise.reject(new Error('auth_failed'));
  const promptId = String(++promptNumber);
  return new Promise((resolve,reject) => {
   const close = () => { const p=prompts.get(promptId); if(!p)return;prompts.delete(promptId);p.cleanup();send({type:'prompt_closed',id:operation.id,promptId});reject(new Error('cancelled')); };
   const cleanup = () => spec.signal?.removeEventListener('abort',close);
   prompts.set(promptId,{resolve,reject,cleanup});spec.signal?.addEventListener('abort',close,{once:true});
   send({type:'prompt',id:operation.id,promptId,kind:'manual_code',message:'等待浏览器回调；必要时提供完整回调 URL'});
  });
 };
 const receive = record => {
  if (ended || operation?.protocolFault) return;
  if (!record || typeof record!=='object' || Array.isArray(record)) { abort('protocol_error');return; }
  if (record.type === 'abort') { if(operation && record.id===operation.id) abort('cancelled');return; }
  if (record.type === 'prompt_reply') {
   if (!operation || record.id!==operation.id) return;
   const pending=prompts.get(record.promptId);if(!pending)return;
   try {
    const url=new URL(record.value);
    if(url.origin!=='http://localhost:1455'||url.pathname!=='/auth/callback'||url.searchParams.get('state')!==authState||!authState||!url.searchParams.get('code'))throw new Error();
    prompts.delete(record.promptId);pending.cleanup();pending.resolve(url.href);
   } catch { fail(new Error('invalid_prompt_reply'));operation.controller.abort(); }
   return;
  }
  if (operation || !['login','refresh'].includes(record.type) || record.provider!=='openai-codex' || typeof record.id!=='string' || !/^[A-Za-z0-9_-]{1,100}$/.test(record.id) || !Number.isSafeInteger(record.timeoutMs) || record.timeoutMs<1 || record.timeoutMs>2147483647) { abort('protocol_error');return; }
  operation={id:record.id,kind:record.type,controller:new AbortController(),started:false,cancelRequested:false};
  timer=setTimeout(()=>abort('expired'),record.timeoutMs);
  void (async()=>{
   try {
    let current;
    if(record.type==='refresh')current=credential(record.credential);
    else await portAvailable(callbackPort);
    if(ended)return;
    const provider=await loadProvider(record.piPackageRoot);
    if(ended)return;
    if(record.type==='login')restore=guardCallbacks(operation.controller.signal,()=>{fail(new Error('callback_port_in_use'));operation.controller.abort();},callbackPort);
    diagnostic({stage:'provider_loaded'});
    operation.started=true;
    const received=await withCodexOAuthScope(()=>record.type==='login'
     ? provider.oauth.login({signal:operation.controller.signal,prompt,notify},{agentName:'loop-now'})
     : provider.oauth.refresh(current,operation.controller.signal),diagnostic);
    diagnostic({stage:'credential_received',...(lastDiagnostic?.httpStatus?{httpStatus:lastDiagnostic.httpStatus}:{})});
    let next;
    try {next=credential(received);}
    catch(error){diagnostic({stage:'credential_received',category:'credential_error',...(lastDiagnostic?.httpStatus?{httpStatus:lastDiagnostic.httpStatus}:{})});throw error;}
    diagnostic({stage:'credential_validated',...(lastDiagnostic?.httpStatus?{httpStatus:lastDiagnostic.httpStatus}:{})});
    if(ended)return;
    if(current && next.accountId!==current.accountId)throw new Error('account_mismatch');
    finish('succeeded',{credential:next,...(operation.cancelRequested ? {cancelRequested:true} : {}),...(operation.protocolFault ? {protocolFault:true} : {})});
   } catch(error) {fail(error);operation.controller.abort();}
  })();
 };
 return { receive, inputStopped:()=>ended||Boolean(operation?.protocolFault), close:()=>abort('cancelled'), protocolError:()=>abort('protocol_error') };
}
