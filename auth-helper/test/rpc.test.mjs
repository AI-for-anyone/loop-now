import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, readFile, rm, stat } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import http from 'node:http';
import { createInterface } from 'node:readline';

const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const pi='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js';
const credential=(generation,expires=Date.now()+3600000)=>({type:'oauth',access:'proof-access-'+generation,refresh:'proof-refresh-'+generation,expires,accountId:'proof-account-A'});
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<1000;i++){if(fn())return;await pause(10);}throw new Error('actual Pi proof timed out');}

test('actual Pi 1.4.0 RPC loads fixed refresh extension: two processes, shared rotation, long tool loop',async t=>{
 const root=await mkdtemp(join(tmpdir(),'loop-auth-rpc-proof-'));let server;const children=[];
 let latest=credential(0,0),rotationCount=0;let chain=Promise.resolve();const gate=[];
 server=http.createServer((req,res)=>{if(req.url!=='/expire'){res.writeHead(404).end();return;}gate.push(res);if(gate.length===2){latest.expires=0;for(const waiter of gate)waiter.end('ok');}});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const gateURL=`http://127.0.0.1:${server.address().port}/expire`;
 try{
  for(let i=0;i<2;i++){
   const home=join(root,String(i));const agent=join(home,'agent');await mkdir(agent,{recursive:true,mode:0o700});await mkdir(join(home,'tmp'),{mode:0o700});await writeFile(join(agent,'auth.json'),JSON.stringify({'openai-codex':credential(0,0)}),{mode:0o600});
   const report=join(home,'network-report');await writeFile(report,'',{mode:0o600});
   const args=['--import',resolve('auth-helper/test/deny-network.mjs'),pi,'--mode','rpc','--no-extensions','-e',resolve('auth-helper/runtime-refresh.mjs'),'-e',resolve('auth-helper/test/rpc-model.mjs'),'--no-skills','--no-prompt-templates','--no-themes','--no-mcp','--no-session','--provider','openai-codex','--model','gpt-6.1-sol','--thinking','off','--tools','proof_expire'];
   const p=spawn(node,args,{cwd:home,env:{HOME:home,TMPDIR:join(home,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production',PI_CODING_AGENT_DIR:agent,PI_OFFLINE:'1',PI_TELEMETRY:'0',AUTH_PROOF_GATE:gateURL,AUTH_PROOF_NETWORK_REPORT:report,LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS:'5000'},stdio:['pipe','pipe','pipe','pipe','pipe']});
   const c={p,agent,report,events:[],errors:'',requests:[]};children.push(c);p.stderr.on('data',b=>c.errors+=b);createInterface({input:p.stdout}).on('line',line=>{try{c.events.push(JSON.parse(line));}catch{}});
   createInterface({input:p.stdio[4]}).on('line',line=>{
    const frame=JSON.parse(line);c.requests.push(frame);
    assert.deepEqual(Object.keys(frame).sort(),['id','method','provider']);assert.equal(frame.method,'refresh');assert.equal(frame.provider,'openai-codex');
    chain=chain.then(async()=>{if(latest.expires<Date.now()+300000){await pause(20);latest=credential(++rotationCount);}p.stdio[3].write(JSON.stringify({id:frame.id,ok:true,credential:latest})+'\n');});
   });
   p.stdin.write(JSON.stringify({id:'prompt-'+i,type:'prompt',message:'offline long round proof'})+'\n');
  }
  await until(()=>children.every(c=>c.events.some(e=>e.type==='agent_end'))||children.some(c=>c.p.exitCode!==null));
  for(const c of children){
   if(!c.events.some(e=>e.type==='agent_end'))assert.fail('Pi ended before round: '+c.errors.slice(0,500));
   const end=c.events.find(e=>e.type==='agent_end');const text=JSON.stringify(end);
   assert(text.includes('offline authenticated'),'actual Pi did not complete authenticated model/tool/model round: '+text.slice(0,500));
   assert(c.requests.length>=2,'long-round second model request did not delegate refresh');
   const stored=JSON.parse(await readFile(join(c.agent,'auth.json'),'utf8'))['openai-codex'];assert.equal(stored.refresh,'proof-refresh-2');assert.equal((await stat(join(c.agent,'auth.json'))).mode&0o777,0o600);
   assert(!JSON.stringify(c.events).includes('proof-access-'));assert.equal(await readFile(c.report,'utf8'),'','supplier request attempts must be zero');
   assert(!JSON.stringify(c.events).includes('proof-refresh-'));assert(!c.errors.includes('proof-refresh-'));
  }
  assert.equal(rotationCount,2,'each expiry wave must refresh once across both actual Pi processes');
 }finally{for(const c of children){c.p.kill('SIGKILL');await new Promise(r=>c.p.exitCode!==null?r():c.p.once('exit',r));}for(const res of gate)res.destroy();await new Promise(r=>server.close(r));await rm(root,{recursive:true,force:true});}
});
