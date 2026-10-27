import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import http from 'node:http';
import { createHelper } from '../helper.mjs';
import { loadPiProvider, credential } from '../pi-provider.mjs';

const piRoot='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent';
const cred={type:'oauth',access:'access-private-marker',refresh:'refresh-private-marker',expires:Date.now()+3600000,accountId:'account-A'};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function eventually(fn){for(let i=0;i<200;i++){if(fn())return;await sleep(5);}throw new Error('condition timed out');}
function harness(oauth,options={}) {const records=[];const helper=createHelper({send:r=>records.push(r),loadProvider:async()=>({oauth}),callbackPort:0,...options});return {helper,records,start:(type='login',timeoutMs=1000)=>helper.receive({type,id:'op1',provider:'openai-codex',timeoutMs,piPackageRoot:piRoot,...(type==='refresh'?{credential:cred}:{})})};}
const result=h=>h.records.find(r=>r.type==='result');

test('installed package public models and canonical credential shape',async()=>{
 const p=await loadPiProvider(piRoot);assert.equal(p.version,'1.4.0');assert(p.models.length>0);assert.equal(typeof p.oauth.login,'function');assert.equal(typeof p.oauth.refresh,'function');assert.equal(typeof p.oauth.toAuth,'function');
 assert.deepEqual(credential({...cred,unexpected:'drop'}),cred);
 assert.throws(()=>credential({...cred,access:'a'.repeat(65536)}));
 await assert.rejects(loadPiProvider('/does-not-exist'));
});
test('notify/prompt/reply is state-bound; secrets appear only in result',async()=>{
 const h=harness({async login(i){assert.equal(await i.prompt({type:'select',options:[{id:'browser'}]}),'browser');i.notify({type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=state-marker'});i.notify({type:'progress',message:cred.access});await i.prompt({type:'manual_code'});return cred;}});h.start();
 await eventually(()=>h.records.some(r=>r.type==='prompt'));
 const prompt=h.records.find(r=>r.type==='prompt');h.helper.receive({type:'prompt_reply',id:'op1',promptId:prompt.promptId,value:'http://localhost:1455/auth/callback?code=code-private-marker&state=state-marker'});
 await eventually(()=>result(h));assert.equal(result(h).status,'succeeded');assert.deepEqual(result(h).credential,cred);
 const ordinary=JSON.stringify(h.records.filter(r=>r.type!=='result'));assert(!ordinary.includes(cred.access));assert(!ordinary.includes(cred.refresh));assert(!ordinary.includes('code-private-marker'));
});
test('invalid manual state, raw provider errors and unsafe authorize URL are not echoed',async()=>{
 for(const oauth of [
  {async login(){throw new Error(cred.refresh);}},
  {async login(i){i.notify({type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=s&access_token='+cred.access});return cred;}},
  {async login(i){i.notify({type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=s'});await i.prompt({type:'manual_code'});return cred;}},
 ]){const h=harness(oauth);h.start();await eventually(()=>result(h)||h.records.some(r=>r.type==='prompt'));const prompt=h.records.find(r=>r.type==='prompt');if(prompt)h.helper.receive({type:'prompt_reply',id:'op1',promptId:prompt.promptId,value:'code-private-marker'});await eventually(()=>result(h));assert.equal(result(h).status,'failed');assert(!JSON.stringify(h.records).includes(cred.access));assert(!JSON.stringify(h.records).includes(cred.refresh));}
});
test('login cancellation, timeout and per-prompt abort suppress late credentials',async()=>{
 for(const mode of ['cancel','timeout','prompt-abort']) {
  let release,started=false;const h=harness({async login(i){started=true;if(mode==='prompt-abort'){const c=new AbortController();const p=i.prompt({type:'manual_code',signal:c.signal});c.abort();await assert.rejects(p);throw new Error('cancelled');}return new Promise(r=>release=r);}});
  h.start('login',mode==='timeout'?20:1000);await eventually(()=>started);
  if(mode==='cancel')h.helper.receive({type:'abort',id:'op1'});
  await eventually(()=>result(h));if(release)release(cred);await sleep(10);
  assert.equal(h.records.filter(r=>r.type==='result').length,1);assert(!result(h).credential);
 }
});
test('refresh cancellation does not discard rotated token; timeout is uncertain',async()=>{
 let release,started=false;const h=harness({async refresh(){started=true;return new Promise(r=>release=r);}});h.start('refresh');await eventually(()=>started);h.helper.receive({type:'abort',id:'op1'});assert(!result(h));release({...cred,refresh:'rotated-marker'});await eventually(()=>result(h));assert.equal(result(h).credential.refresh,'rotated-marker');assert.equal(result(h).cancelRequested,true);
 const uncertain=harness({async refresh(){return new Promise(()=>{});}});uncertain.start('refresh',20);await eventually(()=>result(uncertain));assert.equal(result(uncertain).code,'refresh_uncertain');
 const mismatch=harness({async refresh(){return {...cred,accountId:'account-B'};}});mismatch.start('refresh');await eventually(()=>result(mismatch));assert.equal(result(mismatch).code,'account_mismatch');assert(!result(mismatch).credential);
});

test('actual installed Pi login callback: offline fetch stub, wrong state, abort cleanup and port TOCTOU',async t=>{
 // Real fixed endpoint only on loopback. If owned by another program, never kill it.
 const probe=createServer();try{await new Promise((r,j)=>{probe.once('error',j);probe.listen(1455,'127.0.0.1',r);});}catch(e){if(e.code==='EADDRINUSE'){t.skip('1455 already owned; no process disturbed');return;}throw e;}await new Promise(r=>probe.close(r));
 const originalFetch=globalThis.fetch;let exchanges=0;
 const jwt='x.'+Buffer.from(JSON.stringify({'https://api.openai.com/auth':{chatgpt_account_id:'account-A'}})).toString('base64')+'.x';
 globalThis.fetch=async(input)=>{assert.equal(String(input),'https://auth.openai.com/oauth/token');exchanges++;return new Response(JSON.stringify({access_token:jwt,refresh_token:cred.refresh,expires_in:3600}),{status:200});};
 const provider=await loadPiProvider(piRoot);
 // Avoid PKCE's asynchronous node:crypto initialization race before login.
 await import(provider.aiRoot+'/dist/auth/oauth/openai-codex.js');await sleep(10);
 const request=url=>new Promise((resolve,reject)=>{http.get(url,r=>{r.resume();r.on('end',()=>resolve(r.statusCode));}).on('error',reject);});
 try {
  const h=harness(null,{loadProvider:async()=>provider,callbackPort:1455});h.start();await eventually(()=>h.records.some(r=>r.type==='notify'));
  const url=new URL(h.records.find(r=>r.type==='notify').event.url),state=url.searchParams.get('state');
  assert.equal(await request('http://127.0.0.1:1455/auth/callback?code=fake-code&state=wrong'),400);
  assert(!result(h));assert.equal(exchanges,0);
  assert.equal(await request('http://127.0.0.1:1455/auth/callback?code=fake-code&state='+state),200);
  await eventually(()=>result(h));assert.equal(result(h).status,'succeeded');assert.equal(result(h).credential.accountId,'account-A');assert.equal(exchanges,1);
  const cancelled=harness(null,{loadProvider:async()=>provider,callbackPort:1455});cancelled.start();await eventually(()=>cancelled.records.some(r=>r.type==='notify'));cancelled.helper.receive({type:'abort',id:'op1'});await eventually(()=>result(cancelled));assert.equal(result(cancelled).status,'cancelled');await sleep(10);
  const timed=harness(null,{loadProvider:async()=>provider,callbackPort:1455});timed.start('login',20);await eventually(()=>result(timed));assert.equal(result(timed).status,'expired');await sleep(10);
  const owner=createServer();await new Promise(r=>owner.listen(1455,'127.0.0.1',r));
  try {const occupied=harness(null,{loadProvider:async()=>{throw new Error('must not load');},callbackPort:1455});occupied.start();await eventually(()=>result(occupied));assert.equal(result(occupied).code,'callback_port_in_use');}finally{await new Promise(r=>owner.close(r));}
  // Occupy after preflight but before actual Pi login binds its callback.
  let raceOwner;
  const race=harness(null,{callbackPort:1455,loadProvider:async()=>{raceOwner=createServer();await new Promise(r=>raceOwner.listen(1455,'127.0.0.1',r));return provider;}});race.start();await eventually(()=>result(race));assert.equal(result(race).code,'callback_port_in_use');assert(!race.records.some(r=>r.type==='notify'));assert.equal(exchanges,1);await new Promise(r=>raceOwner.close(r));
 } finally {globalThis.fetch=originalFetch;}
});


test('started refresh protocol fault/second command/EOF retain the unique late rotation',async()=>{
 for(const mode of ['bad-frame','second-command','eof']){
  let release,started=false;
  const h=harness({async refresh(){started=true;return new Promise(r=>release=r);}});h.start('refresh');await eventually(()=>started);
  if(mode==='bad-frame')h.helper.protocolError();if(mode==='second-command')h.helper.receive({type:'login'});if(mode==='eof')h.helper.close();
  assert(!result(h));release({...cred,refresh:'only-rotated-marker'});await eventually(()=>result(h));
  assert.equal(result(h).status,'succeeded');assert.equal(result(h).credential.refresh,'only-rotated-marker');assert.equal(result(h).cancelRequested,true);
  if(mode!=='eof')assert.equal(result(h).protocolFault,true);
 }
});
