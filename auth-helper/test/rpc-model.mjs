// Test-only explicit extension. No provider/authorization network calls.
import { readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import http from 'node:http';
const root='/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent';
const { createAssistantMessageEventStream }=await import(pathToFileURL(join(root,'node_modules/@earendil-works/pi-ai/dist/utils/event-stream.js')).href);
export default function install(pi){
 let calls=0;
 pi.registerTool({name:'proof_expire',label:'proof',description:'offline expiration proof',parameters:{type:'object',properties:{}},async execute(){
  const path=join(process.env.PI_CODING_AGENT_DIR,'auth.json');
  const auth=JSON.parse(await readFile(path,'utf8'));auth['openai-codex'].expires=0;
  await writeFile(path,JSON.stringify(auth),{mode:0o600});
  await new Promise((resolve,reject)=>{http.get(process.env.AUTH_PROOF_GATE,response=>{response.resume();response.on('end',resolve);}).on('error',reject);});
  return {content:[{type:'text',text:'expiry simulated'}],details:{}};
 }});
 pi.registerProvider('openai-codex',{baseUrl:'https://chatgpt.com/backend-api',api:'openai-codex-responses',streamSimple(model,context,options){
  if(!options.apiKey?.startsWith('proof-access-'))throw new Error('offline auth proof failed');
  const stream=createAssistantMessageEventStream();const first=++calls===1;
  const message={role:'assistant',content:first?[{type:'toolCall',id:'proof-call',name:'proof_expire',arguments:{}}]:[{type:'text',text:'offline authenticated'}],api:model.api,provider:model.provider,model:model.id,usage:{input:1,output:1,cacheRead:0,cacheWrite:0,totalTokens:2,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:first?'toolUse':'stop',timestamp:Date.now()};
  queueMicrotask(()=>{stream.push({type:'start',partial:message});stream.push({type:'done',reason:message.stopReason,message});stream.end();});return stream;
 }});
}
