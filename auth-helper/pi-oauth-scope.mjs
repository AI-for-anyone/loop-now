// One operation, one private helper process, fixed openai-codex provider only.
// Do not use this adapter inside an Agent/Pi runtime or a shared OAuth service.
export async function withCodexOAuthScope(call, diagnostic = () => {}) {
 const originalAtob=globalThis.atob, originalFetch=globalThis.fetch;
 globalThis.atob=function(value){
  if(typeof value==='string' && /^[A-Za-z0-9_-]*={0,2}$/.test(value))value=value.replace(/-/g,'+').replace(/_/g,'/');
  return originalAtob(value);
 };
 globalThis.fetch=async function(input,init){
  const target=typeof input==='string'||input instanceof URL?String(input):input?.url;
  if(target!=='https://auth.openai.com/oauth/token')return originalFetch.call(this,input,init);
  diagnostic({stage:'exchange_started'});
  let response;
  try {response=await originalFetch.call(this,input,init);}
  catch(error){
   const category=init?.signal?.aborted||error?.name==='TimeoutError'?'timeout_error':
    ['CERT_HAS_EXPIRED','DEPTH_ZERO_SELF_SIGNED_CERT','UNABLE_TO_VERIFY_LEAF_SIGNATURE','ERR_TLS_CERT_ALTNAME_INVALID'].includes(error?.cause?.code)?'tls_error':'network_error';
   diagnostic({stage:'exchange_started',category});throw error;
  }
  const httpStatus=Number.isInteger(response.status)&&response.status>=100&&response.status<=599?response.status:undefined;
  diagnostic({stage:'exchange_http',...(httpStatus?{httpStatus}:{}),...(!response.ok?{category:'http_error'}:{})});
  const json=response.json.bind(response);
  response.json=async()=>{
   let value;
   try {value=await json();}
   catch(error){diagnostic({stage:'exchange_json',category:'json_error',...(httpStatus?{httpStatus}:{})});throw error;}
   const accessIsString=typeof value?.access_token==='string'&&value.access_token.length>0;
   const refreshIsString=typeof value?.refresh_token==='string'&&value.refresh_token.length>0;
   const expiresIsNumber=typeof value?.expires_in==='number'&&Number.isFinite(value.expires_in);
   diagnostic({stage:'exchange_json',...(httpStatus?{httpStatus}:{}),accessIsString,refreshIsString,expiresIsNumber,
    ...(!(accessIsString&&refreshIsString&&expiresIsNumber)?{category:'credential_error'}:{})});
   return value;
  };
  return response;
 };
 try {return await call();}
 finally {globalThis.atob=originalAtob;globalThis.fetch=originalFetch;}
}
