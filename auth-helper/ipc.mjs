import { Socket } from 'node:net';
import { TextDecoder } from 'node:util';
import { createHelper, MAX_RECORD } from './helper.mjs';

export function runIPC(options = {}) {
 // Parent also discards fd1/2. Never turn private protocol records/errors into logs.
 process.stdout.write = () => true;
 process.stderr.write = () => true;
 let input, output;
 try { input=new Socket({fd:3,readable:true,writable:false});output=new Socket({fd:4,readable:false,writable:true}); }
 catch { process.exit(2); }
 const drainTimeoutMs=options.drainTimeoutMs ?? Number(process.env.LOOP_NOW_AUTH_HELPER_DRAIN_MS ?? 5000);
 if(!Number.isSafeInteger(drainTimeoutMs)||drainTimeoutMs<1||drainTimeoutMs>2147483647)process.exit(2);
 let queued=0, terminal=false, drainTimer;
 const send = record => {
  if(terminal)return;
  let line, forcedTerminal=false;
  try {
   line=JSON.stringify(record)+'\n';
   if(Buffer.byteLength(line)>MAX_RECORD){forcedTerminal=true;line=JSON.stringify({type:'result',id:record.id??null,status:'failed',code:'invalid_credential'})+'\n';}
  } catch { process.exit(2); }
  if(++queued>32)process.exit(2);
  drainTimer ??= setTimeout(()=>process.exit(2),drainTimeoutMs);
  if(record.type==='result'||forcedTerminal)terminal=true;
  output.write(line,()=>{queued--;if(queued===0){clearTimeout(drainTimer);drainTimer=undefined;}if(terminal&&queued===0){input.destroy();output.end(()=>process.exit(0));}});
 };
 const helper=createHelper({...options,send});
 process.on('uncaughtException',()=>{helper.protocolError();});
 process.on('unhandledRejection',()=>{helper.protocolError();});
 output.on('error',()=>process.exit(2));
 input.on('error',()=>helper.protocolError());
 input.on('end',()=>{if(pending.length)helper.protocolError();else helper.close();});
 let pending=Buffer.alloc(0);
 const decoder=new TextDecoder('utf-8',{fatal:true});
 input.on('data',chunk=>{
  if(terminal||helper.inputStopped())return;
  pending=Buffer.concat([pending,chunk]);
  while(!terminal){
   const end=pending.indexOf(10);
   if(end<0){if(pending.length>MAX_RECORD){helper.protocolError();pending=Buffer.alloc(0);input.pause();}break;}
   if(end>MAX_RECORD){helper.protocolError();pending=Buffer.alloc(0);input.pause();break;}
   const frame=pending.subarray(0,end);pending=pending.subarray(end+1);
   try {helper.receive(JSON.parse(decoder.decode(frame)));} catch {helper.protocolError();}
   if(helper.inputStopped()){pending=Buffer.alloc(0);input.pause();break;}
  }
 });
 send({type:'ready',protocolVersion:1,expectedPiVersion:process.env.LOOP_NOW_AUTH_PI_VERSION ?? '1.4.0'});
}
