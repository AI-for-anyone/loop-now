import { runIPC } from '../ipc.mjs';
runIPC({callbackPort:0,loadProvider:async mode=>({oauth:{
 async login(i){
  if(Object.keys(process.env).some(k=>k.startsWith('LOOP_NOW_MASTER')||k==='OPENAI_API_KEY'))throw new Error('environment leak');
  console.log('access-private-marker');console.error('refresh-private-marker');
  if(mode==='fail')throw new Error('refresh-private-marker');
  if(mode==='drain'){for(let n=0;n<25;n++)i_dummy();function i_dummy(){i.notify({type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state='+('s'.repeat(15000))});}}
  if(mode==='late')await new Promise(r=>setTimeout(r,60));
  if(mode==='prompt'){i.notify({type:'auth_url',url:'https://auth.openai.com/oauth/authorize?state=state-marker'});await i.prompt({type:'manual_code'});}
  return {type:'oauth',access:'access-private-marker',refresh:'refresh-private-marker',expires:Date.now()+3600000,accountId:'A'};
 },
 async refresh(c){await new Promise(r=>setTimeout(r,30));return {...c,refresh:'rotated-private-marker'};},
}})});
