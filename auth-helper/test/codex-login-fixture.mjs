// Offline fixture only: the actual installed lazy provider runs, but fetch never
// reaches a socket. The parent also applies a process network sandbox.
import { runIPC } from '../ipc.mjs';
const mode=process.env.AUTH_FIXTURE_MODE;
const accountId='fixture-account';
const payload={'https://api.openai.com/auth':{chatgpt_account_id:accountId}};
if(mode==='base64url'||mode==='refresh')payload.extra='\u083e\u083f';
if(mode==='missing-account')delete payload['https://api.openai.com/auth'];
const jwt='x.'+Buffer.from(JSON.stringify(payload)).toString(mode==='ok'?'base64':'base64url')+'.x';
globalThis.fetch=async(input,init)=>{
 if(String(input)!=='https://auth.openai.com/oauth/token'||init?.method!=='POST')throw new Error('fixture_unexpected_request');
 // Deliberately separate the successful callback page from exchange completion.
 await new Promise(r=>setTimeout(r,40));
 if(mode==='transport')throw new TypeError('synthetic-private-provider-body');
 if(mode==='http401')return new Response('synthetic-private-provider-body',{status:401});
 if(mode==='invalid-json')return new Response('synthetic-private-provider-body',{status:200});
 return new Response(JSON.stringify({access_token:jwt,refresh_token:'synthetic-private-refresh',expires_in:mode==='fractional'||mode==='refresh'?3600.0005:3600}),{status:200});
};
runIPC();
