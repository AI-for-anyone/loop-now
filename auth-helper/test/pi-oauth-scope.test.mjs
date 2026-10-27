import { test } from 'node:test';
import assert from 'node:assert/strict';
import { withCodexOAuthScope } from '../pi-oauth-scope.mjs';
import { credential } from '../pi-provider.mjs';

test('fixed-provider base64url scope retains original validation and always restores globals',async()=>{
 const originalAtob=globalThis.atob, originalFetch=globalThis.fetch;
 const payload=Buffer.from('\u083e\u083f').toString('base64url');
 assert.throws(()=>originalAtob(payload)); // prove fixture exercises URL alphabet
 for(const fail of [false,true]){
  try {await withCodexOAuthScope(async()=>{
   assert.equal(atob(payload),Buffer.from('\u083e\u083f').toString('latin1'));
   assert.equal(atob('YQ=='),'a');assert.throws(()=>atob('bad?'));assert.throws(()=>atob('a'));
   if(fail)throw new Error('synthetic-failure');
  });}catch(error){assert.equal(error.message,'synthetic-failure');}
  assert.equal(globalThis.atob,originalAtob);assert.equal(globalThis.fetch,originalFetch);
 }
});
test('canonical expires floors without extending validity and rejects invalid range',()=>{
 const c={type:'oauth',access:'synthetic-access',refresh:'synthetic-refresh',accountId:'synthetic-account',expires:1234.75};
 assert.equal(credential(c).expires,1234);
 assert.equal(credential({...c,expires:Number.MAX_SAFE_INTEGER}).expires,Number.MAX_SAFE_INTEGER);
 for(const expires of [NaN,Infinity,-1,0,0.5,Number.MAX_SAFE_INTEGER+1])assert.throws(()=>credential({...c,expires}));
});
