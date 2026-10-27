// Capability probe only, no Pi/SDK/provider import and no external addresses.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtemp,mkdir,readFile,writeFile,rm } from 'node:fs/promises';
import { tmpdir,platform,release } from 'node:os';
import { join,resolve } from 'node:path';

const node='/Users/ljl/.local/node-v24.21.0/bin/node';
const report={purpose:'loopback-only process sandbox capability',platform:platform(),release:release(),nativeSDKExecuted:false,piExecuted:false,externalAddressProbed:false,profile:null,baseline:null,child:null,allowedHits:0,deniedHits:0,capabilityPassed:false};
const allowed=http.createServer((req,res)=>{report.allowedHits++;res.end('allowed fixture');});
const denied=http.createServer((req,res)=>{report.deniedHits++;res.end('denied fixture');});
const home=await mkdtemp(join(tmpdir(),'loop-sandbox-capability-'));
async function listen(server){await new Promise((r,j)=>{server.once('error',j);server.listen(0,'127.0.0.1',r);});return server.address().port;}
function request(port){return new Promise((r,j)=>{http.get({host:'127.0.0.1',port,path:'/probe',timeout:1000},response=>{response.resume();response.on('end',()=>r(response.statusCode));}).on('error',j);});}
try {
 await mkdir(join(home,'tmp'),{mode:0o700});
 const a=await listen(allowed),b=await listen(denied);
 report.ports={allowed:a,denied:b};
 report.baseline={allowed:await request(a),denied:await request(b)};
 report.allowedHits=0;report.deniedHits=0;
 const profile=`(version 1)\n(allow default)\n(deny network*)\n(allow network-outbound (remote ip "localhost:${a}"))\n`;
 report.profile=profile;
 const child=spawn('/usr/bin/sandbox-exec',['-p',profile,node,resolve('auth-helper/test/sandbox-port-probe.mjs'),String(a),String(b)],{cwd:home,env:{HOME:home,TMPDIR:join(home,'tmp'),PATH:'/usr/bin:/bin',NODE_ENV:'production'},stdio:['ignore','pipe','pipe']});
 let stdout='',stderr='';child.stdout.on('data',b=>stdout+=b);child.stderr.on('data',b=>stderr+=b);
 const timer=setTimeout(()=>child.kill('SIGKILL'),5000);
 const status=await new Promise(r=>{child.once('error',error=>r({spawnErrorCode:error.code??'SPAWN_FAILED'}));child.once('exit',(code,signal)=>r({exitCode:code,signal}));});clearTimeout(timer);
 let parsed;try{parsed=JSON.parse(stdout);}catch{}
 report.child={...status,result:parsed??null,stderr:stderr.slice(0,4096)};
 report.capabilityPassed=status.exitCode===0&&parsed?.allowed?.status===200&&['EPERM','EACCES'].includes(parsed?.denied?.errorCode)&&report.allowedHits===1&&report.deniedHits===0;
 report.next=report.capabilityPassed?'Main review required before get_state-only/native probe':'stop native probe; no JS fallback; get_state-only not run without effective process isolation';
} finally {
 await Promise.all([allowed,denied].map(server=>new Promise(r=>server.close(r))));await rm(home,{recursive:true,force:true});
 try{const old=JSON.parse(await readFile(resolve('auth-helper/sandbox-capability-result.json'),'utf8'));report.previousAttempts=[...(old.previousAttempts??[]),{profile:old.profile,ports:old.ports,baseline:old.baseline,child:old.child,allowedHits:old.allowedHits,deniedHits:old.deniedHits,capabilityPassed:old.capabilityPassed}];}catch{}
 await writeFile(resolve('auth-helper/sandbox-capability-result.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
}
process.stdout.write(JSON.stringify(report,null,2)+'\n');
