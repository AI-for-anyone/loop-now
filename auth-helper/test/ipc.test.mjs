import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mkdtemp, mkdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { resolve,join } from 'node:path';
import { PassThrough, Writable } from 'node:stream';
import { createRefreshClient,FRAME_LIMIT } from '../refresh-client.mjs';

const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<300;i++){if(fn())return;await pause(5);}throw new Error('IPC test timed out');}
async function child(t){const root=await mkdtemp(join(tmpdir(),'loop-auth-ipc-'));await mkdir(join(root,'tmp'),{mode:0o700});
 const p=spawn(node,[resolve('auth-helper/test/fake-helper.mjs')],{cwd:root,env:{HOME:root,TMPDIR:join(root,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production'},stdio:['pipe','pipe','pipe','pipe','pipe']});const c={p,records:[],stdout:'',stderr:'',write:r=>p.stdio[3].write(JSON.stringify(r)+'\n')};
 p.stdout.on('data',b=>c.stdout+=b);p.stderr.on('data',b=>c.stderr+=b);p.stdio[3].on('error',()=>{});createInterface({input:p.stdio[4]}).on('line',line=>c.records.push(JSON.parse(line)));
 t.after(async()=>{if(p.exitCode===null){p.kill('SIGKILL');await new Promise(r=>p.once('exit',r));}await rm(root,{recursive:true,force:true});});await until(()=>c.records.some(r=>r.type==='ready'));return c;}
const start=(mode='ok',timeoutMs=1000)=>({type:'login',id:'op',provider:'openai-codex',timeoutMs,piPackageRoot:mode});
const terminal=c=>c.records.find(r=>r.type==='result');
test('real helper subprocess uses only private IPC; provider log/error secrets suppressed',async t=>{
 for(const mode of ['ok','fail']){const c=await child(t);c.p.stdin.end('not protocol');c.write(start(mode));await until(()=>terminal(c));await until(()=>c.p.exitCode!==null);assert.equal(terminal(c).status,mode==='ok'?'succeeded':'failed');assert.equal(c.stdout,'');assert.equal(c.stderr,'');const publicFrames=JSON.stringify(c.records.filter(r=>r.type!=='result'));assert(!publicFrames.includes('private-marker'));if(mode==='fail')assert(!JSON.stringify(terminal(c)).includes('private-marker'));}
});
test('real IPC cancellation/timeout suppresses delayed secret results',async t=>{
 for(const mode of ['cancel','timeout']){const c=await child(t);c.write(start('late',mode==='timeout'?10:1000));if(mode==='cancel')c.write({type:'abort',id:'op'});await until(()=>terminal(c));await until(()=>c.p.exitCode!==null);assert.equal(terminal(c).status,mode==='cancel'?'cancelled':'expired');assert(!terminal(c).credential);assert.equal(c.records.filter(r=>r.type==='result').length,1);}
});
test('private IPC validates malformed, scalar, UTF8 and oversized input without echo',async t=>{
 for(const data of ['{"secret":"private-marker"\n','123\n',Buffer.from([0xff,10]),Buffer.alloc(FRAME_LIMIT+1,97)]){const c=await child(t);c.p.stdio[3].write(data);await until(()=>terminal(c));assert.equal(terminal(c).status,'protocol_error');assert(!JSON.stringify(c.records).includes('private-marker'));}
});
function client(timeoutMs=100){const input=new PassThrough(),output=new PassThrough();const frames=[];createInterface({input:output}).on('line',line=>frames.push(JSON.parse(line)));return {input,output,frames,client:createRefreshClient({input,output,timeoutMs})};}
const cred={type:'oauth',access:'access-private-marker',refresh:'refresh-private-marker',expires:Date.now()+3600000,accountId:'A'};
test('refresh channel matches IDs, ignores late reply and keeps cancellation local',async()=>{
 const c=client();try{
  const control=new AbortController();const first=c.client.request(control.signal);control.abort();await assert.rejects(first);assert.deepEqual(Object.keys(c.frames[0]).sort(),['id','method','provider']);
  c.input.write(JSON.stringify({id:c.frames[0].id,ok:true,credential:cred})+'\n');
  const second=c.client.request();c.input.write(JSON.stringify({id:c.frames[1].id,ok:true,credential:cred})+'\n');assert.deepEqual(await second,cred);assert.equal(c.frames.length,2);
 }finally{c.client.close();}
});
test('refresh channel timeout/disconnect/malformed responses are bounded and sanitized',async()=>{
 for(const mode of ['timeout','eof','bad','oversize','remote-error']){
  const c=client(15);const waiting=c.client.request();
  if(mode==='eof')c.input.end();if(mode==='bad')c.input.write('private-marker\n');if(mode==='oversize')c.input.write(Buffer.alloc(FRAME_LIMIT+1,97));if(mode==='remote-error')c.input.write(JSON.stringify({id:'1',ok:false,code:'refresh-private-marker'})+'\n');
  await assert.rejects(waiting,error=>!error.message.includes('private-marker'));c.client.close();
 }
});
test('refresh channel bounds pending requests and backpressure does not defeat timeout',async()=>{
 const c=client(15);c.output.pause();const pending=[];for(let i=0;i<33;i++)pending.push(c.client.request());const done=await Promise.allSettled(pending);assert(done.every(r=>r.status==='rejected'));c.client.close();
});


test('helper output not consumed: terminal drain has an independent bounded exit',async t=>{
 const root=await mkdtemp(join(tmpdir(),'loop-auth-drain-'));await mkdir(join(root,'tmp'),{mode:0o700});
 const p=spawn(node,[resolve('auth-helper/test/fake-helper.mjs')],{cwd:root,env:{HOME:root,TMPDIR:join(root,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production',LOOP_NOW_AUTH_HELPER_DRAIN_MS:'80'},stdio:['ignore','ignore','ignore','pipe','pipe']});
 p.stdio[3].on('error',()=>{});
 t.after(async()=>{if(p.exitCode===null){p.kill('SIGKILL');await new Promise(r=>p.once('exit',r));}await rm(root,{recursive:true,force:true});});
 p.stdio[3].write(JSON.stringify(start('drain'))+'\n');
 await until(()=>p.exitCode!==null);assert.equal(p.exitCode,2);
});

test('refresh transport bounds unflushed writes even after many batches time out',async()=>{
 const input=new PassThrough();const output=new Writable({highWaterMark:1,write(chunk,encoding,callback){/* deliberately never acknowledge */}});
 let writes=0;const write=output.write.bind(output);output.write=(...args)=>{writes++;return write(...args);};
 const c=createRefreshClient({input,output,timeoutMs:20});
 for(let batch=0;batch<4;batch++){const promises=[];for(let n=0;n<40;n++)promises.push(c.request());const results=await Promise.allSettled(promises);assert(results.every(r=>r.status==='rejected'));}
 assert(writes<=32);assert(output.destroyed);c.close();
});
