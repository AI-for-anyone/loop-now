// Replaces the unsafe issuer/preload body. Every case is sandboxed before CLI
// initialization and targets only a fresh parent-owned loopback HTTP fixture.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mkdtemp,mkdir,writeFile,readFile,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join,resolve } from 'node:path';

const root='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent';
const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<500;i++){if(fn())return;await pause(10);}throw new Error('Bounded local Pi proof timeout');}
async function listen(server){await new Promise((r,j)=>{server.once('error',j);server.listen(0,'127.0.0.1',r);});return server.address().port;}
async function baseline(port){return new Promise((r,j)=>{http.get({host:'127.0.0.1',port,path:'/probe',timeout:1000},res=>{res.resume();res.on('end',()=>r(res.statusCode));}).on('error',j);});}
function sse(){
 const events=[
  {type:'message_start',message:{id:'msg_fixture',type:'message',role:'assistant',content:[],model:'offline',stop_reason:null,stop_sequence:null,usage:{input_tokens:1,output_tokens:0}}},
  {type:'content_block_start',index:0,content_block:{type:'text',text:''}},
  {type:'content_block_delta',index:0,delta:{type:'text_delta',text:'loopback Anthropic authenticated'}},
  {type:'content_block_stop',index:0},
  {type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:1}},
  {type:'message_stop'},
 ];
 return events.map(e=>`event: ${e.type}\ndata: ${JSON.stringify(e)}\n\n`).join('');
}
async function stop(child){if(child&&child.exitCode===null){child.kill('SIGKILL');await new Promise(r=>child.once('exit',r));}}
async function sandboxProbe(profile,home,a,b){
 const p=spawn('/usr/bin/sandbox-exec',['-p',profile,node,resolve('auth-helper/test/sandbox-port-probe.mjs'),String(a),String(b)],{cwd:home,env:{HOME:home,TMPDIR:join(home,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production'},stdio:['ignore','pipe','pipe']});
 let stdout='',stderr='';p.stdout.on('data',b=>stdout+=b);p.stderr.on('data',b=>stderr+=b);
 const timer=setTimeout(()=>p.kill('SIGKILL'),3000);
 const exit=await new Promise(r=>{p.once('error',()=>r({spawnFailed:true}));p.once('exit',(code,signal)=>r({code,signal}));});clearTimeout(timer);
 let response;try{response=JSON.parse(stdout);}catch{}
 return {...exit,response:response??null,stderrPresent:stderr.length>0};
}

test('sandboxed actual Pi native Anthropic SDK: loopback URL, original resolver auth modes, denied peer port',async()=>{
 const directory=await mkdtemp(join(tmpdir(),'loop-anthropic-native-'));
 const report={purpose:'actual Pi + original Anthropic client against loopback only',piVersion:'1.1.0',jsFetchPatchUsed:false,externalAddressProbed:false,passed:false,cases:[]};
 const metadata=JSON.parse(await readFile(join(root,'node_modules/@earendil-works/pi-ai/dist/providers/data/anthropic.json'),'utf8'));
 const model=Object.values(metadata).flatMap(group=>Object.values(group)).find(m=>m.type==='chat').id;
 const cases=[
  ['bearer-single',{ANTHROPIC_AUTH_TOKEN:'synthetic-bearer'},'bearer','synthetic-bearer'],
  ['oauth-field-single',{ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth'},'api-key','synthetic-oauth'],
  ['api-single',{ANTHROPIC_API_KEY:'synthetic-api'},'api-key','synthetic-api'],
  ['bearer-wins-three',{ANTHROPIC_AUTH_TOKEN:'synthetic-bearer',ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth',ANTHROPIC_API_KEY:'synthetic-api'},'bearer','synthetic-bearer'],
  ['oauth-field-wins-two',{ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth',ANTHROPIC_API_KEY:'synthetic-api'},'api-key','synthetic-oauth'],
  // The original native API recognizes this prefix after the resolver returns
  // apiKey. Preserve that behavior too; this is still a synthetic credential.
  ['oauth-prefix-native-bearer',{ANTHROPIC_OAUTH_TOKEN:'sk-ant-oat-synthetic-oauth'},'bearer','sk-ant-oat-synthetic-oauth'],
 ];
 try{
  for(let i=0;i<cases.length;i++){
   const [name,env,mode,expected]=cases[i];
   const record={name,mode,profile:null,preflight:null,model:null,promptSent:false,fixturePaths:[],modelRequests:0,authMatched:true,deniedHits:0,allowedProbeHits:0,passed:false};report.cases.push(record);
   let allowedPort;
   const allowed=http.createServer((req,res)=>{
    req.resume();
    if(req.url==='/probe'){record.allowedProbeHits++;res.end('fixture');return;}
    record.modelRequests++;record.fixturePaths.push(req.url);
    // beta=true is pinned in installed @anthropic-ai/sdk beta/messages source.
    if(req.method!=='POST'||req.headers.host!==`127.0.0.1:${allowedPort}`||req.url!=='/v1/messages?beta=true') {record.authMatched=false;res.writeHead(400).end('Unexpected local fixture route');return;}
    const matched=mode==='bearer'?(req.headers.authorization===`Bearer ${expected}`&&!req.headers['x-api-key']):(req.headers['x-api-key']===expected&&!req.headers.authorization);
    if(!matched){record.authMatched=false;res.writeHead(401,{'content-type':'application/json'}).end(JSON.stringify({type:'error',error:{type:'authentication_error',message:'Local fixture auth mismatch'}}));return;}
    res.writeHead(200,{'content-type':'text/event-stream','request-id':'local-fixture'});res.end(sse());
   });
   const denied=http.createServer((req,res)=>{record.deniedHits++;req.resume();res.end('denied-port fixture');});
   let p;
   try{
    allowedPort=await listen(allowed);const deniedPort=await listen(denied);
    record.ports={allowed:allowedPort,denied:deniedPort};
    assert.equal(await baseline(allowedPort),200);assert.equal(await baseline(deniedPort),200);record.allowedProbeHits=0;record.deniedHits=0;
    const home=join(directory,String(i)),agent=join(home,'agent');await mkdir(agent,{recursive:true,mode:0o700});await mkdir(join(home,'tmp'),{mode:0o700});
    const baseUrl=`http://127.0.0.1:${allowedPort}`;
    await writeFile(join(agent,'models.json'),JSON.stringify({providers:{anthropic:{baseUrl}}}),{mode:0o600});
    await writeFile(join(agent,'auth.json'),JSON.stringify({anthropic:{type:'api_key',env}}),{mode:0o600});
    const profile=`(version 1)\n(allow default)\n(deny network*)\n(allow network-outbound (remote ip "localhost:${allowedPort}"))\n`;record.profile=profile;
    // Fresh OS-level capability proof on these exact endpoints before each CLI.
    record.preflight=await sandboxProbe(profile,home,allowedPort,deniedPort);
    assert.equal(record.preflight.code,0,'Sandbox unavailable; stop without fallback');
    assert.equal(record.preflight.response?.allowed?.status,200,'Allowed fixture unreachable; stop');
    assert(['EPERM','EACCES'].includes(record.preflight.response?.denied?.errorCode),'Peer port not denied; stop');
    assert.equal(record.allowedProbeHits,1);assert.equal(record.deniedHits,0);assert.equal(record.modelRequests,0);
    p=spawn('/usr/bin/sandbox-exec',['-p',profile,node,join(root,'dist/bundle/cli.js'),'--mode','rpc','--no-extensions','-e',resolve('auth-helper/legacy-anthropic.mjs'),'--no-skills','--no-prompt-templates','--no-themes','--no-mcp','--no-tools','--no-session','--provider','anthropic','--model',model,'--thinking','off'],{cwd:home,env:{HOME:home,TMPDIR:join(home,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production',PI_CODING_AGENT_DIR:agent,PI_OFFLINE:'1',PI_TELEMETRY:'0',LOOP_NOW_PI_PACKAGE_ROOT:root},stdio:['pipe','pipe','pipe']});
    const events=[];let stderr='';p.stdin.on('error',()=>{});p.stderr.on('data',b=>stderr+=b);createInterface({input:p.stdout}).on('line',line=>{try{events.push(JSON.parse(line));}catch{}});
    p.stdin.write(JSON.stringify({id:'state',type:'get_state'})+'\n');
    await until(()=>events.some(e=>e.type==='response'&&e.id==='state')||p.exitCode!==null);
    const state=events.find(e=>e.type==='response'&&e.id==='state');assert(state?.success,'Sandboxed Pi state unavailable; stop');
    const m=state.data?.model;record.model=m?{provider:m.provider,api:m.api,id:m.id,baseUrl:m.baseUrl}:null;
    assert.equal(m?.provider,'anthropic');assert.equal(m?.api,'anthropic-messages');assert.equal(m?.id,model);assert.equal(m?.baseUrl,baseUrl,'Final model URL not local fixture; do not prompt');
    assert.equal(record.modelRequests,0,'No model request permitted before URL check');assert.equal(record.deniedHits,0);
    record.promptSent=true;p.stdin.write(JSON.stringify({id:'prompt',type:'prompt',message:'local SDK authentication proof'})+'\n');
    await until(()=>events.some(e=>e.type==='agent_end')||p.exitCode!==null);
    const end=events.find(e=>e.type==='agent_end');assert(end,'Local model round failed to finish');
    assert(JSON.stringify(end).includes('loopback Anthropic authenticated'),'Original native SDK did not parse local SSE result');
    assert.equal(record.modelRequests,1);assert.equal(record.authMatched,true);assert.equal(record.deniedHits,0);
    for(const secret of Object.values(env)){assert(!JSON.stringify(events).includes(secret),'Synthetic credential exposed by RPC');assert(!stderr.includes(secret),'Synthetic credential exposed by stderr');}
    record.passed=true;
   }finally{await stop(p);await Promise.all([allowed,denied].map(s=>new Promise(r=>s.close(r))));}
  }
  report.passed=report.cases.every(c=>c.passed);
 }finally{
  await rm(directory,{recursive:true,force:true});
  try{const previous=JSON.parse(await readFile(resolve('auth-helper/anthropic-native-result.json'),'utf8'));report.previousRuns=[...(previous.previousRuns??[]),{passed:previous.passed,cases:previous.cases}];}catch{}
  await writeFile(resolve('auth-helper/anthropic-native-result.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
 }
});
