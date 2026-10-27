/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { mkdirSync, writeFileSync } from 'node:fs'
import assert from 'node:assert/strict'
const origin='http://localhost:5173'
const out=new URL('../test-results/add-project-credentials/popup-hidden/',import.meta.url)
mkdirSync(out,{recursive:true})
const phase=process.env.PROJECT_LOGIN_POPUP_PHASE??'after'
const now = '2026-10-15T01:00:00Z', id = suffix => `10000000-0000-7000-8000-${suffix.padStart(12, '0')}`
const project = { id: id('1'), organizationId: id('2'), name: '凭证验证项目', isDefault: false, managerAgentId: null, agentCount: 1, createdAt: now, updatedAt: now }
const defaultProject = { ...project, id: id('3'), name: '默认项目', isDefault: true, agentCount: 0 }
const templates = [{ name: 'gh', version: '2.100.0', fields: [{ name: 'token', label: '访问令牌', type: 'secret', required: true }], injection: 'env', install: { status: 'installed' }, warnings: [] }]
const providerList = { piVersion: '1.1.0', catalogGeneratedAt: null, providers: [{ id: 'anthropic', apiKeyEnvVars: [], modelCount: 1, authMethods: ['api_key'] }, { id: 'openai-codex', apiKeyEnvVars: [], modelCount: 1, authMethods: ['oauth'] }] }
const limits = { maxUploadFile: 1000000, maxMessage: 65536, maxInboxMessage: 65536, maxSkillArchive: 1000000, maxSkillEntries: 100, maxCredential: 65536, textEdit: { maxBytes: 1000000, extensions: ['.md', '.txt'] } }
const base = `/p/${encodeURIComponent(project.name)}`

let current=null, readyAt=0
const operation={id:id('20'),projectId:project.id,provider:'openai-codex',targetAccountId:null,targetRevision:null,name:'离线导航验证',status:'pending',accountId:null,startedAt:now,expiresAt:'2099-01-01T00:00:00Z',completedAt:null,authorizationUrl:null,errorCode:null,errorMessage:null}
// All values are synthetic; no actual operation or authorization URL is loaded.
const syntheticUrl=new URL('https://auth.openai.com/oauth/authorize')
for(const [key,value]of Object.entries({response_type:'code',client_id:'offline',redirect_uri:'http://localhost:1455/auth/callback',scope:'openid profile email offline_access',code_challenge:'offline',code_challenge_method:'S256',state:'offline',id_token_add_organizations:'true',codex_cli_simplified_flow:'true',originator:'pi'}))syntheticUrl.searchParams.set(key,value)
if(phase==='rejected')syntheticUrl.searchParams.set('access_token','fixture-rejected')
const report={phase,kind:'actual React + offline browser HTTP fixture; every API/provider request fulfilled locally',starts:0,detailReads:0,collectionReads:0,authNavigation:[],unhandled:[],pageErrors:[]}
const browser=await chromium.launch({headless:true})
try{
 const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.route('**/*',async route=>{
  const req=route.request(),u=new URL(req.url()),method=req.method()
  if(u.origin==='https://auth.openai.com'){
   report.authNavigation.push({origin:u.origin,path:u.pathname})
   return route.fulfill({status:200,contentType:'text/html',body:'<!doctype html><title>Offline authorization navigation</title><p>Offline navigation only</p>'})
  }
  if(!u.pathname.startsWith('/api/v1/')){if(u.origin===origin)return route.continue();report.unhandled.push({method,origin:u.origin,path:u.pathname});return route.abort()}
  const json=(value,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(value),headers:{'Access-Control-Allow-Origin':origin}})
  if(method==='OPTIONS')return route.fulfill({status:204,headers:{'Access-Control-Allow-Origin':origin,'Access-Control-Allow-Headers':'content-type','Access-Control-Allow-Methods':'GET,POST'}})
  const path=u.pathname.slice('/api/v1'.length)
  if(path==='/inbox/events')return route.fulfill({status:204,headers:{'Access-Control-Allow-Origin':origin}})
  if(path==='/health')return json({ok:true,postgres:{ok:true,error:null},redis:{ok:true,error:null}})
  if(path==='/context')return json({organization:{id:project.organizationId,name:'测试组织'},project:{id:project.id,name:project.name},limits})
  if(path==='/projects')return json({projects:[defaultProject,project]})
  if(path.endsWith('/prompt'))return json({prompt:'',promptVersion:1})
  if(/^\/projects\/[^/]+\/agents$/.test(path))return json({agents:[]})
  if(path==='/model-providers')return json(providerList)
  if(path==='/cli-templates')return json({templates})
  if(path.endsWith('/credential-accounts'))return json({projectId:project.id,credentialsRevision:1,accounts:[]})
  if(path.endsWith('/credential-account-impact'))return json({projectId:project.id,credentialsRevision:1,accountId:null,agents:[]})
  if(path.endsWith('/model-auth/operations')&&method==='POST'){report.starts++;current={...operation};readyAt=Date.now()+2200;return json({operation:current,account:null},202)}
  if(path.endsWith('/model-auth/operations')){report.collectionReads++;return json({operations:current?[current]:[]})}
  if(path.endsWith('/model-auth/operations/'+operation.id)){
   report.detailReads++
   if(Date.now()>=readyAt&&current.status==='pending')current={...operation,status:'awaiting_authorization',authorizationUrl:syntheticUrl.href}
   return json({operation:current,account:null})
  }
  report.unhandled.push({method,path});return json({error:{code:'not_found',message:'fixture missing'}},404)
 })
 await context.addInitScript(()=>{window.__fixtureHidden=false;Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>window.__fixtureHidden?'hidden':'visible'});Object.defineProperty(document,'hidden',{configurable:true,get:()=>window.__fixtureHidden})})
 const page=await context.newPage();page.on('pageerror',()=>report.pageErrors.push('pageerror'))
 await page.goto(origin+base+'/settings')
 await page.getByLabel('添加模型账号 · 提供方').selectOption('openai-codex')
 await page.getByLabel('新 Codex 账号名称').fill(operation.name)
 await page.waitForFunction(()=>[...document.querySelectorAll('button')].some(b=>b.textContent==='登录 Codex'&&!b.disabled))
 const opened=context.waitForEvent('page');await page.getByRole('button',{name:'登录 Codex',exact:true}).click();const popup=await opened
 await page.waitForTimeout(100)
 await page.evaluate(()=>{window.__fixtureHidden=true;window.dispatchEvent(new Event('visibilitychange'))})
 report.pendingWindowFeedback=await popup.getByText('正在准备授权页，请稍候。返回控制台可查看进度或继续本次登录。',{exact:true}).isVisible()
 report.parentVisibility=await page.evaluate(()=>document.visibilityState)
 report.detailReadsBeforeDelay=report.detailReads
 report.collectionReadsBeforeDelay=report.collectionReads
 await page.waitForTimeout(3800)
 report.detailReadsAfterDelay=report.detailReads
 report.collectionReadsAfterDelay=report.collectionReads
 report.blankWhileHidden=!popup.isClosed()&&popup.url()==='about:blank'
 if(phase==='hidden-before'){
  assert.equal(report.parentVisibility,'hidden');assert.equal(report.blankWhileHidden,true)
  assert.equal(report.detailReadsAfterDelay,report.detailReadsBeforeDelay)
  await page.evaluate(()=>{window.__fixtureHidden=false;window.dispatchEvent(new Event('visibilitychange'))})
  await popup.waitForURL(u=>u.origin==='https://auth.openai.com'&&u.pathname==='/oauth/authorize')
  report.navigatedOnlyAfterFocus=true
 }else{
  assert.equal(report.pendingWindowFeedback,true)
  assert.equal(report.parentVisibility,'hidden');assert.ok(report.detailReadsAfterDelay>report.detailReadsBeforeDelay)
  assert.equal(report.blankWhileHidden,false)
  await popup.waitForURL(u=>u.origin==='https://auth.openai.com'&&u.pathname==='/oauth/authorize')
  report.navigatedWithoutFocus=true
  const beforeTerminal=report.detailReads
  current={...current,status:'expired',authorizationUrl:null,errorCode:'auth_timeout'}
  await page.waitForTimeout(1800)
  const settledReads=report.detailReads
  const settledCollection=report.collectionReads
  await page.waitForTimeout(1800)
  report.terminalPollingStopped=report.detailReads===settledReads&&report.collectionReads===settledCollection
  report.providerWindowPreserved=!popup.isClosed()
  assert.equal(report.providerWindowPreserved,true)
  assert.equal(await page.getByRole('link',{name:'打开授权页',exact:true}).count(),0)
  assert.ok(settledReads>beforeTerminal);assert.equal(report.terminalPollingStopped,true)
  assert.equal(await page.getByLabel('新 Codex 账号名称').isEnabled(),true)
 }
 assert.equal(report.starts,1);assert.deepEqual(report.unhandled,[]);assert.deepEqual(report.pageErrors,[])
 report.passed=true;await page.getByRole('region',{name:'模型账号',exact:true}).screenshot({path:new URL(phase+'-fixture.png',out).pathname})
 writeFileSync(new URL(phase+'-fixture.json',out),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report))
}finally{await browser.close()}
