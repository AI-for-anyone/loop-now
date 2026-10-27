import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mkdtemp,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve,join } from 'node:path';
import http from 'node:http';
import { createServer } from 'node:net';

const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const piRoot='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent';
const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let n=0;n<600;n++){if(fn())return;await delay(5);}throw new Error('offline login fixture timed out');}
test('actual cold lazy Pi login, synthetic exchange, private fd terminal flush and ABI boundaries',async t=>{
 const probe=createServer();
 try{await new Promise((r,j)=>{probe.once('error',j);probe.listen(1455,'127.0.0.1',r);});}
 catch(e){if(e.code==='EADDRINUSE'){t.skip('callback port occupied; owner untouched');return;}throw e;}
 await new Promise(r=>probe.close(r));
 for(const mode of ['ok','fractional','base64url','http401','transport','invalid-json','missing-account','refresh'])await t.test(mode,async t=>{
  const home=await mkdtemp(join(tmpdir(),'loop-auth-codex-offline-'));
  const profile='(version 1) (allow default) (deny network*) (allow network-bind (local ip "localhost:1455")) (allow network-inbound (local ip "localhost:1455"))';
  const p=spawn('/usr/bin/sandbox-exec',['-p',profile,node,resolve('auth-helper/test/codex-login-fixture.mjs')],{cwd:home,env:{HOME:home,TMPDIR:home,PATH:'/usr/bin:/bin',NODE_ENV:'production',AUTH_FIXTURE_MODE:mode},stdio:['ignore','pipe','pipe','pipe','pipe']});
  const frames=[];let stdout='',stderr='';
  p.stdout.on('data',b=>stdout+=b);p.stderr.on('data',b=>stderr+=b);p.stdio[3].on('error',()=>{});
  const closed=new Promise(r=>p.once('close',r));
  createInterface({input:p.stdio[4]}).on('line',line=>frames.push(JSON.parse(line)));
  t.after(async()=>{if(p.exitCode===null){p.kill('SIGKILL');await closed;}await rm(home,{recursive:true,force:true});});
  await until(()=>frames.some(f=>f.type==='ready')||p.exitCode!==null);assert.equal(p.exitCode,null,'sandbox/ready preflight failed');
  p.stdio[3].write(JSON.stringify({type:mode==='refresh'?'refresh':'login',id:'fixture',provider:'openai-codex',timeoutMs:2500,piPackageRoot:piRoot,...(mode==='refresh'?{credential:{type:'oauth',access:'synthetic-old-access',refresh:'synthetic-old-refresh',expires:1,accountId:'fixture-account'}}:{})})+'\n');
  if(mode!=='refresh'){
  await until(()=>frames.some(f=>f.type==='notify'&&f.event?.type==='auth_url')||frames.some(f=>f.type==='result'));
  const notify=frames.find(f=>f.type==='notify'&&f.event?.type==='auth_url');assert.ok(notify,'actual Pi did not reach callback wait');
  const state=new URL(notify.event.url).searchParams.get('state');
  const status=await new Promise((r,j)=>http.get('http://127.0.0.1:1455/auth/callback?code=synthetic-code&state='+state,res=>{res.resume();res.on('end',()=>r(res.statusCode));}).on('error',j));
  assert.equal(status,200);
  }
  await closed;
  assert.equal(p.exitCode,0);assert.equal(stdout,'');assert.equal(stderr,'');
  const results=frames.filter(f=>f.type==='result');assert.equal(results.length,1);
  const result=results[0];
  assert.equal(result.status,['ok','fractional','base64url','refresh'].includes(mode)?'succeeded':'failed');
  if(mode==='ok')assert.equal(Number.isSafeInteger(result.credential.expires),true);
  if(mode==='fractional')assert.equal(Number.isSafeInteger(result.credential.expires),true,'canonical integer milliseconds');
  if(mode==='base64url')assert.equal(result.credential.accountId,'fixture-account');
  const diagnostics=frames.filter(f=>f.type==='diagnostic');
  if(mode!=='refresh')assert(diagnostics.some(f=>f.stage==='exchange_started'));
  else {assert.equal(diagnostics.length,0);assert.equal(result.credential.accountId,'fixture-account');assert.equal(Number.isSafeInteger(result.credential.expires),true);}
  if(mode==='http401')assert(diagnostics.some(f=>f.stage==='exchange_http'&&f.httpStatus===401&&f.category==='http_error'));
  if(mode==='transport')assert(diagnostics.some(f=>f.stage==='exchange_started'&&f.category==='network_error'));
  if(mode==='invalid-json')assert.equal(result.diagnostic.category,'json_error');
  assert(!JSON.stringify(frames.filter(f=>f.type!=='result')).includes('synthetic-private'));
  assert(!JSON.stringify(result.status==='failed'?result:{}).includes('synthetic-private'));
 });
});
