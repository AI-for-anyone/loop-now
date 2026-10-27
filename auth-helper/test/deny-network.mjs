// Test-only preload runs before CLI initialization, including early getAuth.
// No URLs/bodies are persisted: record only the count of forbidden attempts.
import { appendFileSync } from 'node:fs';
const original=globalThis.fetch;
const gate=new URL(process.env.AUTH_PROOF_GATE);
globalThis.fetch=async(input,options)=>{
 const url=new URL(typeof input==='string'||input instanceof URL?input:input.url);
 if(url.origin===gate.origin&&url.pathname===gate.pathname)return original(input,options);
 appendFileSync(process.env.AUTH_PROOF_NETWORK_REPORT,'external_attempt\n',{mode:0o600});
 throw new Error('External network blocked by offline Pi test');
};
