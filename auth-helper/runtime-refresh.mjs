import { credential } from './pi-provider.mjs';
import { inheritedRefreshClient } from './refresh-client.mjs';

// Only explicitly loaded platform extension. stdin/stdout remain Pi's RPC;
// fd3/4 are an independent private refresh channel bound by the parent process.
export default function install(pi) {
 const client=inheritedRefreshClient();
 pi.registerProvider('openai-codex',{
  baseUrl:'https://chatgpt.com/backend-api',
  oauth:{
   name:'OpenAI (ChatGPT Plus/Pro)',isSubscription:true,
   async login(){throw new Error('请从控制台登录 Codex');},
   async refreshToken(current,signal){
    const next=await client.request(signal);
    if(next.accountId!==current.accountId)throw new Error('Codex 账号与运行绑定不匹配');
    return next;
   },
   getApiKey(value){return credential(value).access;},
  },
 });
}
