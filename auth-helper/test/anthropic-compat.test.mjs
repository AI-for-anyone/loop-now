// Capability proof only: not an installed runtime adapter or migration.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { anthropicProvider } from '/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/providers/anthropic.js';

test('installed Anthropic resolver can preserve old bearer/multiple-key priority using per-call in-memory AuthContext',async()=>{
 const resolver=anthropicProvider().auth.apiKey.resolve;
 const keys=new Set(['ANTHROPIC_AUTH_TOKEN','ANTHROPIC_OAUTH_TOKEN','ANTHROPIC_API_KEY']);
 for(const values of [
  {ANTHROPIC_API_KEY:'synthetic-api'},
  {ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth'},
  {ANTHROPIC_AUTH_TOKEN:'synthetic-bearer'},
  {ANTHROPIC_AUTH_TOKEN:'synthetic-bearer',ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth',ANTHROPIC_API_KEY:'synthetic-api'},
  {ANTHROPIC_OAUTH_TOKEN:'synthetic-oauth',ANTHROPIC_API_KEY:'synthetic-api'},
 ]){
  const signal=new AbortController().signal;
  const old=await resolver({ctx:{env:async name=>values[name],fileExists:async()=>false},signal});
  const stored={type:'api_key',env:{...values}};
  const wrapped=await resolver({credential:stored,ctx:{env:async name=>keys.has(name)?stored.env[name]:undefined,fileExists:async()=>false},signal});
  assert.deepEqual(wrapped,old);
 }
});
