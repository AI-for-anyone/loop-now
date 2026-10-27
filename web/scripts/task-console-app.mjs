/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { randomUUID, createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'
const m=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_TASK_CONSOLE_MANIFEST,'utf8')),api=new URL(m.apiBase),web=new URL(m.webOrigin)
assert.equal(api.hostname,'127.0.0.1');assert.equal(api.protocol,'http:');assert.equal(api.pathname,'/api/v1');assert.ok(!['8780','8781','5173',''].includes(api.port));assert.equal(web.origin,'http://127.0.0.1:4320');assert.ok(!api.search&&!api.hash&&!api.username&&!api.password)
const out=new URL('../test-results/improve-task-console-interaction/app/',import.meta.url);mkdirSync(out,{recursive:true})
const root=fileURLToPath(new URL('../',import.meta.url)),checks=[],posts=[],errors=[],denied=[],controls=[],consoleKinds=[]
let browser,preview,forwarded=[]
const inputs={};for(const path of ['../api/openapi.yaml','src/api/schema.gen.ts','src/features/tasks/NodeMessageComposer.tsx','scripts/task-console-app.mjs'])inputs[path]=createHash('sha256').update(readFileSync(root+path)).digest('hex')
const startPreview=()=>new Promise((resolve,reject)=>{preview=spawn(process.execPath,[root+'node_modules/vite/bin/vite.js','--host','127.0.0.1','--port',web.port,'--strictPort'],{cwd:root,env:{PATH:process.env.PATH},stdio:['ignore','pipe','pipe']});const timeout=setTimeout(()=>reject(new Error('temporary frontend not ready')),10000);preview.stdout.on('data',buffer=>{if(buffer.toString().includes(web.origin)){clearTimeout(timeout);resolve()}});preview.once('exit',()=>{clearTimeout(timeout);reject(new Error('frontend exited'))})})
try{
 await startPreview();browser=await chromium.launch({headless:true});const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.route('**/*',route=>{const q=route.request(),url=new URL(q.url());if(![web.origin,api.origin,'http://127.0.0.1:8780'].includes(url.origin)||!['GET','HEAD','OPTIONS'].includes(q.method())&&!url.pathname.endsWith(`/tasks/${m.taskId}/nodes/${m.nodeRunId}/messages`)){denied.push({method:q.method(),path:url.pathname});return route.abort()}return route.fallback()})
 const page=await context.newPage();page.on('pageerror',()=>errors.push('pageerror'));page.on('console',event=>{if(event.type()==='error')consoleKinds.push('console.error; expected HTTP/HMR details not persisted')})
 forwarded=await forwardTemporaryAPI(page,m.apiBase,web.origin)
 const get=async path=>{const r=await context.request.get(m.apiBase+path,{headers:{origin:web.origin},maxRetries:0,maxRedirects:0});assert.equal(r.status(),200);return r.json()}
 let cancelled=false
 await page.route(`**/api/v1/tasks/${m.taskId}/nodes/${m.nodeRunId}/messages`,async route=>{
  const q=route.request();if(q.method()!=='POST')return route.fallback()
  const body=q.postDataJSON();const index=posts.length+1
  if(index===4){const response=await context.request.post(m.apiBase+`/tasks/${m.taskId}/cancel`,{headers:{origin:web.origin},data:{actionId:randomUUID()},maxRetries:0,maxRedirects:0});assert.equal(response.status(),200);controls.push({method:'POST',path:`/tasks/${m.taskId}/cancel`,status:200,mode:'explicit TEST fixture control before held original-node POST'});cancelled=true}
  const destination=api.origin+new URL(q.url()).pathname
  const response=await route.fetch({url:destination,headers:{...q.headers(),origin:web.origin,host:api.host},maxRetries:0,maxRedirects:0,timeout:15000})
  const json=await response.json();posts.push({index,status:response.status(),actionId:body.actionId,whenBusy:body.whenBusy,nodeRunId:m.nodeRunId,expectedAgentId:body.expectedAgentId,expectedRuntimeId:body.expectedRuntimeId,receiptRunId:json.runId??null,receiptMessageId:json.message?.id??null,bodyText:body.text,mode:'route.fetch actual isolated App response',version:response.headers()['loop-now-version']??null})
  if(index===2){assert.equal(response.status(),201);return route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:{code:'internal_error',message:'TEST transport discarded actual 201 receipt'}})})}
  return route.fulfill({response})
 })
 await page.goto(web.origin+`/p/${encodeURIComponent(m.projectName)}/tasks/${m.taskId}`)
 const input=page.getByRole('textbox',{name:'消息草稿'}),send=()=>page.getByRole('button',{name:'发送',exact:true})
 await input.waitFor();for(let i=0;i<100&&!await send().isEnabled();i++)await page.waitForTimeout(100)
 assert.equal(await send().isEnabled(),true)
 let detail=await get(`/tasks/${m.taskId}`);assert.equal(detail.nodes.find(n=>n.id===m.nodeRunId).question.canAnswer,false);assert.equal(detail.nodes.find(n=>n.id===m.nodeRunId).question.status,'open')
 await input.fill('!sleep=10s !text=web-console-queue');await send().click();await page.getByText('上次消息已接收并进入队列。').waitFor()
 assert.equal(posts[0].status,201);assert.equal(posts[0].whenBusy,'queue')
 const messages=()=>get(`/tasks/${m.taskId}/nodes/${m.nodeRunId}/messages`)
 let queue
 for(let i=0;i<150;i++){queue=(await messages()).messages.find(v=>v.runId===posts[0].receiptRunId&&v.role==='assistant');if(queue?.status==='running')break;await page.waitForTimeout(100)}
 assert.equal(queue?.status,'running')
 checks.push('actual UI queue POST201 fixed target; actual GET observes original-node assistant run running')
 await page.getByRole('combobox',{name:'忙时处理'}).selectOption('interrupt');await input.fill('!sleep=0s !text=web-console-interrupt-reply');await send().click()
 await page.getByRole('button',{name:'重试确认原消息'}).waitFor();assert.equal(posts.length,2);assert.equal(posts[1].status,201);assert.equal(posts[1].whenBusy,'interrupt')
 await input.fill('new draft while original receipt is unknown');await page.getByRole('button',{name:'重试确认原消息'}).click()
 await page.getByRole('button',{name:'发送',exact:true}).waitFor();assert.equal(posts.length,3);assert.equal(posts[2].status,201);assert.equal(posts[1].actionId,posts[2].actionId);assert.equal(posts[1].receiptRunId,posts[2].receiptRunId);assert.equal(posts[1].receiptMessageId,posts[2].receiptMessageId);assert.equal(await input.inputValue(),'new draft while original receipt is unknown')
 let reply
 for(let i=0;i<150;i++){reply=(await messages()).messages.find(v=>v.runId===posts[1].receiptRunId&&v.role==='assistant');if(reply?.status==='completed')break;await page.waitForTimeout(100)}
 assert.equal(reply?.status,'completed');assert.equal(reply.nodeRunId,m.nodeRunId)
 await page.getByText('web-console-interrupt-reply',{exact:true}).waitFor()
 detail=await get(`/tasks/${m.taskId}`);const node=detail.nodes.find(n=>n.id===m.nodeRunId);assert.equal(detail.task.currentNodeRunId,m.nodeRunId);assert.equal(node.question.status,'open');assert.equal(node.question.canAnswer,false);assert.equal(node.attempt,1)
 checks.push('actual interrupt run reply via GET and real SSE-driven React refresh; lost actual 201 manually replayed same action/receipt once; new draft retained; Manager question remains open/canAnswer=false')
 for(const viewport of [{width:1440,height:1000},{width:390,height:844}]){await page.setViewportSize(viewport);await page.locator('[data-node-draft]').scrollIntoViewIfNeeded();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false);await page.screenshot({path:new URL(`accepted-${viewport.width}.png`,out).pathname})}
 await input.fill('keep original node draft on real terminal race');await send().click()
 for(let i=0;i<100&&posts.length<4;i++)await page.waitForTimeout(100)
 assert.equal(posts.length,4);assert.equal(posts[3].status,409);assert.equal(cancelled,true)
 await page.getByText(/原任务节点或运行条件已变化/).waitFor();assert.equal(await input.inputValue(),'keep original node draft on real terminal race');assert.equal(await input.getAttribute('readonly'),'')
 await page.locator('[data-node-draft]').scrollIntoViewIfNeeded();await page.screenshot({path:new URL('conflict-390.png',out).pathname})
 assert.equal(posts.filter(p=>p.actionId===posts[1].actionId).length,2);assert.ok(posts.every(p=>p.expectedAgentId===m.agentId&&p.expectedRuntimeId===m.runtimeId));assert.ok(forwarded.some(p=>p.path.endsWith(`/tasks/${m.taskId}/events`)&&p.status===200))
 checks.push('actual cancel control races held original-node POST409; no fallback/new action, original draft becomes readonly, no new step/attempt target')
 assert.deepEqual(errors,[]);assert.deepEqual(denied,[])
 writeFileSync(new URL('browser-results.json',out),JSON.stringify({boundary:'actual React -> real isolated App/TEST PG/fakePi; response-discard only for unknown receipt; no actual current service/provider',taskId:m.taskId,nodeRunId:m.nodeRunId,checks,posts,controls,forwarded,errors,denied,consoleKinds,inputs},null,2))
}catch(error){writeFileSync(new URL('browser-failure.json',out),JSON.stringify({errorType:error.name,sourceLine:error.stack?.match(/task-console-app\.mjs:\d+:\d+/)?.[0]??null,checks,posts,controls,forwarded,errors,denied,consoleKinds,inputs},null,2));process.exitCode=1}
finally{await browser?.close();if(preview&&preview.exitCode===null){preview.kill('SIGTERM');await new Promise(resolve=>{const timeout=setTimeout(resolve,3000);preview.once('exit',()=>{clearTimeout(timeout);resolve()})})}}
