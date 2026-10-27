// Metadata-only Pi RPC probe. Never sends prompt or invokes a native model API.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { mkdtemp,mkdir,readFile,writeFile,rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join,resolve } from 'node:path';
const root='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent';
const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const capability=JSON.parse(await readFile(resolve('auth-helper/sandbox-capability-result.json'),'utf8'));
if(capability.capabilityPassed!==true)throw new Error('Effective process isolation has not been verified');
const result={purpose:'actual Pi get_state-only loopback model URL',piVersion:'1.1.0',promptSent:false,nativeModelRequestSent:false,externalAddressProbed:false,profile:null,state:null,fixtureHits:0,deniedFixtureHits:0,passed:false};
const allowed=http.createServer((req,res)=>{result.fixtureHits++;res.writeHead(500).end('No model request authorized');});
const denied=http.createServer((req,res)=>{result.deniedFixtureHits++;res.writeHead(500).end('Denied fixture');});
const home=await mkdtemp(join(tmpdir(),'loop-anthropic-state-'));let child;
async function listen(server){await new Promise((r,j)=>{server.once('error',j);server.listen(0,'127.0.0.1',r);});return server.address().port;}
try{
 const port=await listen(allowed),deniedPort=await listen(denied),baseUrl=`http://127.0.0.1:${port}`;
 result.ports={allowed:port,denied:deniedPort};result.expectedBaseUrl=baseUrl;
 const agent=join(home,'agent');await mkdir(agent,{mode:0o700});await mkdir(join(home,'tmp'),{mode:0o700});
 await writeFile(join(agent,'auth.json'),JSON.stringify({anthropic:{type:'api_key',env:{ANTHROPIC_AUTH_TOKEN:'synthetic-state-only'}}}),{mode:0o600});
 await writeFile(join(agent,'models.json'),JSON.stringify({providers:{anthropic:{baseUrl}}}),{mode:0o600});
 const metadata=JSON.parse(await readFile(join(root,'node_modules/@earendil-works/pi-ai/dist/providers/data/anthropic.json'),'utf8'));
 const model=Object.values(metadata).flatMap(group=>Object.values(group)).find(m=>m.type==='chat').id;
 const profile=`(version 1)\n(allow default)\n(deny network*)\n(allow network-outbound (remote ip "localhost:${port}"))\n`;
 result.profile=profile;
 child=spawn('/usr/bin/sandbox-exec',['-p',profile,node,join(root,'dist/bundle/cli.js'),'--mode','rpc','--no-extensions','-e',resolve('auth-helper/legacy-anthropic.mjs'),'--no-skills','--no-prompt-templates','--no-themes','--no-mcp','--no-tools','--no-session','--provider','anthropic','--model',model,'--thinking','off'],{cwd:home,env:{HOME:home,TMPDIR:join(home,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production',PI_CODING_AGENT_DIR:agent,PI_OFFLINE:'1',PI_TELEMETRY:'0',LOOP_NOW_PI_PACKAGE_ROOT:root},stdio:['pipe','pipe','pipe']});
 let stderr='';child.stderr.on('data',b=>stderr+=b);child.stdin.on('error',()=>{});
 const reply=await new Promise(resolveReply=>{
  let settled=false;
  const finish=value=>{if(settled)return;settled=true;clearTimeout(timer);resolveReply(value);};
  const timer=setTimeout(()=>finish({failure:'bounded startup timeout'}),5000);
  child.once('error',()=>finish({failure:'sandbox/Pi spawn failed'}));child.once('exit',code=>finish({failure:'sandbox/Pi exited before state',exitCode:code}));
  createInterface({input:child.stdout}).on('line',line=>{try{const event=JSON.parse(line);if(event.type==='response'&&event.id==='state-proof')finish(event);}catch{}});
  child.stdin.write(JSON.stringify({id:'state-proof',type:'get_state'})+'\n');
 });
 if(reply.success===true&&reply.data?.model){
  const m=reply.data.model;result.state={provider:m.provider,api:m.api,id:m.id,baseUrl:m.baseUrl};
  result.passed=m.provider==='anthropic'&&m.api==='anthropic-messages'&&m.id===model&&m.baseUrl===baseUrl&&result.fixtureHits===0&&result.deniedFixtureHits===0;
 }else result.failure=reply.failure??'get_state was not successful';
 result.stderrPresent=stderr.length>0;
}finally{
 if(child&&child.exitCode===null){child.kill('SIGKILL');await new Promise(r=>child.once('exit',r));}
 await Promise.all([allowed,denied].map(s=>new Promise(r=>s.close(r))));await rm(home,{recursive:true,force:true});
 try{const old=JSON.parse(await readFile(resolve('auth-helper/anthropic-state-result.json'),'utf8'));result.previousAttempts=[...(old.previousAttempts??[]),{profile:old.profile,state:old.state,passed:old.passed,ports:old.ports,fixtureHits:old.fixtureHits,deniedFixtureHits:old.deniedFixtureHits,phase:old.profile===null?'before Pi spawn: metadata fixture shape':'state probe'}];}catch{}
 await writeFile(resolve('auth-helper/anthropic-state-result.json'),JSON.stringify(result,null,2)+'\n',{mode:0o600});
}
process.stdout.write(JSON.stringify(result,null,2)+'\n');
