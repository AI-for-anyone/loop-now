import { Socket } from 'node:net';
import { TextDecoder } from 'node:util';
import { credential } from './pi-provider.mjs';

export const FRAME_LIMIT=1024*1024;
const failure=()=>new Error('Codex 凭证刷新通道不可用');
export function createRefreshClient({input,output,timeoutMs=15000}) {
 const pending=new Map();let sequence=0,closed=false,buffer=Buffer.alloc(0),writes=0,drainTimer;
 const decoder=new TextDecoder('utf-8',{fatal:true});
 const close=()=>{
  if(closed)return;closed=true;clearTimeout(drainTimer);
  for(const item of pending.values()){item.cleanup();item.reject(failure());}
  pending.clear();input.destroy();output.destroy();
 };
 input.on('error',close);output.on('error',close);input.on('end',close);input.on('close',close);
 input.on('data',chunk=>{
  if(closed)return;buffer=Buffer.concat([buffer,chunk]);
  while(!closed){
   const end=buffer.indexOf(10);
   if(end<0){if(buffer.length>FRAME_LIMIT)close();break;}
   if(end>FRAME_LIMIT){close();break;}
   const line=buffer.subarray(0,end);buffer=buffer.subarray(end+1);
   let r;try{r=JSON.parse(decoder.decode(line));if(!r||typeof r!=='object'||Array.isArray(r)||typeof r.id!=='string'||typeof r.ok!=='boolean')throw new Error();}catch{close();break;}
   const item=pending.get(r.id);if(!item)continue;pending.delete(r.id);item.cleanup();
   try{if(!r.ok)throw new Error();item.resolve(credential(r.credential));}catch{item.reject(failure());}
  }
 });
 return {
  close,
  request(signal){
   if(closed||signal?.aborted||pending.size>=32||writes>=32)return Promise.reject(failure());
   const id=String(++sequence);
   return new Promise((resolve,reject)=>{
    const abort=()=>{const item=pending.get(id);if(!item)return;pending.delete(id);item.cleanup();reject(failure());};
    const timer=setTimeout(abort,timeoutMs);
    const cleanup=()=>{clearTimeout(timer);signal?.removeEventListener('abort',abort);};
    pending.set(id,{resolve,reject,cleanup});signal?.addEventListener('abort',abort,{once:true});
    // Cancellation only stops local waiting. It never asks the coordinator to
    // cancel an in-flight rotation or discard a result that must be committed.
    writes++;drainTimer ??= setTimeout(close,timeoutMs);
    output.write(JSON.stringify({id,method:'refresh',provider:'openai-codex'})+'\n',error=>{writes--;if(writes===0){clearTimeout(drainTimer);drainTimer=undefined;}if(error)close();});
   });
  },
 };
}
export function inheritedRefreshClient() {
 const input=new Socket({fd:3,readable:true,writable:false});
 const output=new Socket({fd:4,readable:false,writable:true});
 input.unref();output.unref();
 const raw=process.env.LOOP_NOW_AUTH_REFRESH_TIMEOUT_MS;
 const timeoutMs=raw===undefined?15000:Number(raw);
 if(!Number.isSafeInteger(timeoutMs)||timeoutMs<1||timeoutMs>2147483647){input.destroy();output.destroy();throw failure();}
 return createRefreshClient({input,output,timeoutMs});
}
