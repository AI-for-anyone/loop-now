/* global document, innerWidth */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './app-forward.mjs'
const fixture=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_AUTH_MANIFEST,'utf8'))
const dir=fileURLToPath(new URL('.',import.meta.url)),checks=[],errors=[],consoleMessages=[],checkpoints=[]
let phase='initialization',providerNavigationsBlocked=0,unexpectedRequests=0
const browser=await chromium.launch({headless:true}),context=await browser.newContext({viewport:{width:1440,height:1000}}),page=await context.newPage()
page.on('pageerror',error=>errors.push(error.name))
page.on('console',message=>consoleMessages.push(message.text()))
await context.route('**/*',async route=>{
 const url=new URL(route.request().url())
 if(url.origin==='http://localhost:5173'){await route.continue();return}
 if(url.hostname==='auth.openai.com')providerNavigationsBlocked++;else unexpectedRequests++
 await route.abort()
 if(url.hostname==='auth.openai.com'){const popup=route.request().frame().page();if(popup!==page)await popup.close();await page.bringToFront()}
})
const forwarded=await forwardTemporaryAPI(page,fixture.apiBase)
const area=()=>page.getByRole('region',{name:'模型账号凭证'}),path=id=>`http://localhost:5173/p/${encodeURIComponent(fixture.projectName)}/agents/${id}/config`
const ok=name=>{checks.push(name);console.log('PASS '+name)}
const request=async(base,path,body)=>{
 const response=await fetch(base+path,{method:body===undefined?'GET':'POST',headers:body===undefined?{Origin:'http://localhost:5173'}:{Origin:'http://localhost:5173','Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body)})
 if(!response.ok)throw new Error('integration request rejected in '+phase)
 return response.json()
}
const state=()=>request(fixture.apiBase,`/agents/${fixture.codexAgentId}/model-auth`)
const checkpoint=async (agentId,expectedAccount='')=>{const value=await request(fixture.controlBase,'/checkpoint?agentId='+agentId+(expectedAccount?'&expectedAccount='+expectedAccount:''));checkpoints.push(value);if(expectedAccount)assert.equal(value.accountMatches,true);return value}
const start=async initial=>{
 await area().getByRole('button',{name:initial?'登录 Codex':'重新登录',exact:true}).click()
 await area().getByText('等待浏览器授权',{exact:true}).waitFor()
 const value=await state();assert.equal(value.operation.status,'awaiting_authorization')
 return value.operation.id
}
try{
 phase='initial actual App configuration'
 await page.goto(path(fixture.codexAgentId));await area().getByRole('button',{name:'登录 Codex'}).waitFor();await area().screenshot({path:dir+'app-initial-desktop.png'})
 ok('actual React config loads actual App catalog, Agent and model-auth state')
 phase='first login account A'
 let id=await start(true)
 await request(fixture.controlBase,'/resolve',{operationId:id,outcome:'success',account:'web-account-A'})
 await page.bringToFront();await area().getByText('本次登录成功',{exact:true}).waitFor();await area().getByText('已登录',{exact:true}).waitFor()
 const accountA=await checkpoint(fixture.codexAgentId,'web-account-A');assert.equal(accountA.accountMatches,true);assert.equal(accountA.stored,true)
 await area().screenshot({path:dir+'app-account-A-desktop.png'});ok('browser start/query reaches real App and PG persists stored account A metadata')
 phase='cancel preserves A'
 id=await start(false);await area().getByRole('button',{name:'取消登录'}).click();await area().getByText('本次登录已取消',{exact:true}).waitFor()
 let current=await state();assert.equal(current.operation.status,'cancelled');assert.equal(current.credential.versionId,accountA.versionId)
 assert.equal((await checkpoint(fixture.codexAgentId,'web-account-A')).versionId,accountA.versionId);ok('real cancel endpoint commits cancelled operation and retains PG account A')
 phase='failure preserves A'
 id=await start(false);await request(fixture.controlBase,'/resolve',{operationId:id,outcome:'fail',account:''})
 await area().getByText('本次登录失败',{exact:true}).waitFor();await area().getByText('已登录',{exact:true}).waitFor()
 current=await state();assert.equal(current.operation.status,'failed');assert.equal(current.credential.versionId,accountA.versionId)
 assert.equal((await checkpoint(fixture.codexAgentId,'web-account-A')).versionId,accountA.versionId)
 await page.setViewportSize({width:390,height:844});await area().screenshot({path:dir+'app-failure-mobile.png'});ok('offline helper failure travels through actual service; UI and PG retain A')
 phase='success saves B'
 id=await start(false);await request(fixture.controlBase,'/resolve',{operationId:id,outcome:'success',account:'web-account-B'})
 await page.bringToFront();await area().getByText('本次登录成功',{exact:true}).waitFor();await area().getByText('已登录',{exact:true}).waitFor()
 const accountB=await checkpoint(fixture.codexAgentId,'web-account-B');assert.equal(accountB.accountMatches,true);assert.equal(accountB.stored,true);assert.notEqual(accountB.versionId,accountA.versionId);assert.equal((await state()).credential.versionId,accountB.versionId)
 await area().screenshot({path:dir+'app-account-B-mobile.png'});ok('successful re-login reads new actual App metadata and new stored PG version B')
 phase='API Key save and input clear'
 await page.goto(path(fixture.keyAgentId));await area().getByRole('button',{name:'录入 API Key'}).click();let dialog=page.getByRole('dialog')
 await dialog.getByLabel('API Key').fill('web-integration-key-sentinel');await dialog.getByRole('button',{name:'保存',exact:true}).click();await dialog.waitFor({state:'hidden'})
 await area().getByText('已设置',{exact:true}).waitFor();const key=await checkpoint(fixture.keyAgentId);assert.equal(key.authMethod,'api_key');assert.equal(key.stored,true)
 assert.equal((await page.locator('body').innerHTML()).includes('web-integration-key-sentinel'),false)
 await area().getByRole('button',{name:'替换 API Key'}).click();dialog=page.getByRole('dialog');assert.equal(await dialog.getByLabel('API Key').inputValue(),'')
 await dialog.getByLabel('API Key').fill('web-integration-cancelled-key');await dialog.getByRole('button',{name:'取消',exact:true}).click();await dialog.waitFor({state:'hidden'})
 await area().getByRole('button',{name:'替换 API Key'}).click();dialog=page.getByRole('dialog');assert.equal(await dialog.getByLabel('API Key').inputValue(),'');await page.keyboard.press('Escape');await dialog.waitFor({state:'hidden'})
 assert.equal((await checkpoint(fixture.keyAgentId)).versionId,key.versionId);ok('API Key PUT reaches actual App/PG; save and cancel clear secret inputs without changing saved key on cancel')
 phase='API Key delete'
 await area().getByRole('button',{name:'删除',exact:true}).click();const confirm=page.getByRole('alertdialog');await confirm.getByRole('button',{name:'删除',exact:true}).click();await confirm.waitFor({state:'hidden'});await area().getByText('未设置',{exact:true}).waitFor()
 assert.equal((await checkpoint(fixture.keyAgentId)).cleared,true);await area().screenshot({path:dir+'app-key-cleared-mobile.png'});ok('confirmed API Key DELETE reaches actual App and PG explicit clear marker')
 phase='security and transport audit'
 assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false)
 const persisted=await page.evaluate(()=>JSON.stringify({...localStorage,...sessionStorage}))
 for(const sentinel of ['web-integration-key-sentinel','web-integration-cancelled-key','web-offline-access-sentinel','web-offline-refresh-sentinel','web-offline-state','web-offline-challenge']){
  assert.equal(persisted.includes(sentinel),false);assert.equal((await page.locator('body').innerHTML()).includes(sentinel),false);assert.equal(consoleMessages.some(line=>line.includes(sentinel)),false)
 }
 assert.equal(providerNavigationsBlocked>=4,true);assert.equal(unexpectedRequests,0);assert.deepEqual(errors,[])
 assert.equal(forwarded.some(r=>r.method==='POST'&&r.path.endsWith('/model-auth/operations')&&r.status===200),true)
 assert.equal(forwarded.some(r=>r.method==='POST'&&r.path.endsWith('/cancel')&&r.status===200),true)
 assert.equal(forwarded.some(r=>r.method==='PUT'&&r.path.endsWith('/model-credential')&&r.status===200),true)
 assert.equal(forwarded.some(r=>r.method==='DELETE'&&r.path.endsWith('/model-credential')&&r.status===204),true)
 for(const version of [accountA.versionId,accountB.versionId])assert.equal(forwarded.some(r=>r.path.endsWith('/model-auth')&&r.credentialVersionId===version&&r.operationStatus==='succeeded'),true)
 ok('route.fetch records actual HTTP responses; provider navigation blocked before network; no secret sentinels in UI/storage/console')
 writeFileSync(dir+'app-browser-results.json',JSON.stringify({mode:'actual React -> route.fetch forwarding -> actual temporary app.Build -> isolated PG; offline AuthLoginHelper DI only; no MSW/API response stubs',checks,forwarded,checkpoints,providerNavigationsBlocked,unexpectedRequests,errors,consoleSecretSentinelsAbsent:true},null,2))
}catch(error){writeFileSync(dir+'app-browser-failure.json',JSON.stringify({phase,errorType:error.name,checks,forwarded,providerNavigationsBlocked,unexpectedRequests,errors},null,2));console.error('Integration failed at phase: '+phase);process.exitCode=1}
finally{await browser.close()}
