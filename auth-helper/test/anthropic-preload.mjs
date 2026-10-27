// Test-only actual Anthropic SDK request stub, installed before Pi initializes.
// It records only counts/modes, never credentials, URLs or request bodies.
import { appendFileSync } from 'node:fs';
const report=process.env.AUTH_PROOF_NETWORK_REPORT;
globalThis.fetch=async(input,options)=>{
 const url=new URL(typeof input==='string'||input instanceof URL?input:input.url);
 if(url.origin!=='https://api.anthropic.com'||url.pathname!=='/v1/messages'){
  appendFileSync(report,'external_attempt\n',{mode:0o600});throw new Error('External fetch blocked by offline Anthropic proof');
 }
 const headers=new Headers(options?.headers??input.headers);
 const mode=process.env.AUTH_PROOF_EXPECT_MODE;
 const expected=mode==='bearer'?'Bearer synthetic-bearer':mode==='oauth'?'synthetic-oauth':'synthetic-api';
 if(mode==='bearer'?(headers.get('authorization')!==expected||headers.has('x-api-key')):headers.get('x-api-key')!==expected){appendFileSync(report,'auth_mismatch\n',{mode:0o600});throw new Error('Offline Anthropic auth mode mismatch');}
 appendFileSync(report,mode+'\n',{mode:0o600});
 const events=[
  {type:'message_start',message:{id:'msg_synthetic',type:'message',role:'assistant',content:[],model:'offline',stop_reason:null,stop_sequence:null,usage:{input_tokens:1,output_tokens:0}}},
  {type:'content_block_start',index:0,content_block:{type:'text',text:''}},
  {type:'content_block_delta',index:0,delta:{type:'text_delta',text:'offline Anthropic authenticated'}},
  {type:'content_block_stop',index:0},
  {type:'message_delta',delta:{stop_reason:'end_turn',stop_sequence:null},usage:{output_tokens:1}},
  {type:'message_stop'},
 ];
 return new Response(events.map(event=>`event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join(''),{status:200,headers:{'content-type':'text/event-stream','request-id':'synthetic-request'}});
};
