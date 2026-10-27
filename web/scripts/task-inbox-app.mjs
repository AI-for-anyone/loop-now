/* global document, innerWidth */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn, execFileSync } from 'node:child_process'
import { randomUUID, createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import { resolve, sep } from 'node:path'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'
const m=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_TASK_INBOX_MANIFEST,'utf8')),api=new URL(m.apiBase),web=new URL(m.webOrigin)
assert.equal(api.hostname,'127.0.0.1');assert.equal(api.protocol,'http:');assert.equal(api.pathname,'/api/v1');assert.ok(!['8780','8781','5173',''].includes(api.port));assert.equal(web.origin,'http://127.0.0.1:4326');assert.ok(!api.username&&!api.password&&!api.search&&!api.hash)
assert.ok(resolve(m.privateAuthPath).startsWith(resolve(m.privateRoot)+sep));assert.ok(resolve(m.cliHome).startsWith(resolve(m.privateRoot)+sep))
const auth=JSON.parse(readFileSync(m.privateAuthPath,'utf8')),agentURL=new URL(auth.url);assert.equal(agentURL.origin,api.origin);assert.equal(agentURL.pathname,'/agent/v1');assert.ok(auth.token)
const root=fileURLToPath(new URL('../',import.meta.url)),out=new URL('../test-results/add-task-node-inbox-chat/app/',import.meta.url);mkdirSync(out,{recursive:true})
const report={boundary:'actual React -> actual forwarded ephemeral App/TEST PG+Redis/fakePi; actual runtime CLI controlled by test, not autonomous model',checks:[],posts:[],controls:[],cli:[],errors:[],console:[],expectedHTTPConsole:[],denied:[],forwarded:[],inputs:{}}
for(const path of ['../api/openapi.yaml','src/api/schema.gen.ts','src/features/tasks/NodeRecordPanel.tsx','src/features/tasks/NodeMessageComposer.tsx','src/api/task-inbox.ts','src/features/tasks/TaskRunRecord.tsx','scripts/task-inbox-app.mjs'])report.inputs[path]=createHash('sha256').update(readFileSync(root+path)).digest('hex')
let browser,context,page,preview,closing=false
const check=(name,value)=>{report.checks.push({name,passed:!!value});assert.ok(value,name)}
const cli=(args)=>{let raw;try{raw=execFileSync(m.cliBin,args,{env:{HOME:m.cliHome,PATH:process.env.PATH,LOOPCTL_AGENT_URL:auth.url,LOOPCTL_AGENT_TOKEN:auth.token},timeout:10000,stdio:['ignore','pipe','pipe']})}catch(e){throw new Error('controlled CLI failed: '+(e.status??e.name),{cause:e})}const value=JSON.parse(raw.toString());return value.data}
const start=()=>new Promise((resolve,reject)=>{preview=spawn(process.execPath,[root+'node_modules/vite/bin/vite.js','--host','127.0.0.1','--port',web.port,'--strictPort'],{cwd:root,env:{PATH:process.env.PATH},stdio:['ignore','pipe','pipe']});const timer=setTimeout(()=>reject(new Error('self-owned frontend unavailable')),12000);preview.stdout.on('data',b=>{if(b.toString().includes(web.origin)){clearTimeout(timer);resolve()}});preview.once('exit',()=>{clearTimeout(timer);reject(new Error('self-owned frontend exited'))})})
try{
 await start();browser=await chromium.launch({headless:true});context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.routeWebSocket('**/*',ws=>{const u=new URL(ws.url());if(u.origin==='ws://127.0.0.1:4326')ws.send(JSON.stringify({type:'connected'}));else{report.denied.push({method:'WS',origin:u.origin,path:u.pathname});ws.close()}})
 await context.route('**/*',route=>{if(closing)return route.abort();const q=route.request(),u=new URL(q.url());if(![web.origin,api.origin,'http://127.0.0.1:8780'].includes(u.origin)||!['GET','HEAD','OPTIONS'].includes(q.method())&&!u.pathname.endsWith(`/tasks/${m.taskId}/nodes/${m.nodeRunId}/inbox-messages`)){report.denied.push({method:q.method(),origin:u.origin,path:u.pathname});return route.abort()}return route.fallback()})
 page=await context.newPage();page.setDefaultTimeout(15000);page.on('pageerror',e=>report.errors.push(e.name));page.on('console',e=>{if(e.type()==='error'){const text=e.text().replace(/https?:\/\/\S+/g,'[url]');if(/^Failed to load resource: the server responded with a status of (502|409)/.test(text))report.expectedHTTPConsole.push(text);else report.console.push(text)}})
 report.forwarded=await forwardTemporaryAPI(page,m.apiBase,web.origin)
 const nodePath=`/tasks/${m.taskId}/nodes/${m.nodeRunId}`
 const get=async path=>{const response=await context.request.get(m.apiBase+path,{headers:{origin:web.origin},maxRetries:0,maxRedirects:0});assert.equal(response.status(),200);return response.json()}
 const inbox=()=>get(nodePath+'/inbox-messages'),detail=()=>get('/tasks/'+m.taskId)
 await page.route(`**/api/v1${nodePath}/inbox-messages`,async route=>{
  const req=route.request();if(req.method()!=='POST')return route.fallback()
  const body=req.postDataJSON(),index=report.posts.length+1
  if(index===3){const response=await context.request.post(m.apiBase+`/tasks/${m.taskId}/cancel`,{headers:{origin:web.origin},data:{actionId:randomUUID()},maxRetries:0,maxRedirects:0});assert.equal(response.status(),200);report.controls.push({path:`/tasks/${m.taskId}/cancel`,status:200,boundary:'TEST fixture control before already-clicked original node POST'})}
  const response=await route.fetch({url:api.origin+new URL(req.url()).pathname,headers:{...req.headers(),origin:web.origin,host:api.host},maxRetries:0,maxRedirects:0,timeout:15000}),value=await response.json()
  report.posts.push({index,status:response.status(),actionId:body.actionId,whenBusy:body.whenBusy,expectedAgentId:body.expectedAgentId,expectedRuntimeId:body.expectedRuntimeId,nodeRunId:m.nodeRunId,messageId:value.message?.id??null,threadId:value.message?.threadId??null,receiptRunId:value.runId??null,mode:'actual isolated App route.fetch response'})
  if(index===2){assert.equal(response.status(),201);return route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:{code:'internal_error',message:'TEST transport discarded actual Inbox 201 receipt'}})})}
  return route.fulfill({response})
 })
 await page.goto(web.origin+`/p/${encodeURIComponent(m.projectName)}/tasks/${m.taskId}`)
 const input=page.getByRole('textbox',{name:'消息草稿'}),send=()=>page.getByRole('button',{name:'发送',exact:true})
 await input.waitFor();for(let i=0;i<120&&!await send().isEnabled();i++)await page.waitForTimeout(100)
 check('same-version real Inbox/capability allow current node input',await send().isEnabled())
 const currentRun=async(exclude=null)=>{for(let i=0;i<100;i++){const d=await detail();for(const id of [...d.nodes.find(n=>n.id===m.nodeRunId).runIds].reverse()){if(id===exclude)continue;const timeline=await get(nodePath+`/runs/${id}/timeline`);if(timeline.run.status==='running')return id}await page.waitForTimeout(50)}throw Error('actual original-node active turn unavailable')}
 const readReply=async(postIndex,text,exclude=null)=>{
  const post=report.posts[postIndex],items=(await inbox()).items,record=items.find(r=>r.message.id===post.messageId)
  check('browser GET does not mark Agent message read '+postIndex,record.message.readAt===null)
  const list=cli(['inbox','list']);check('real runtime CLI lists original strict Inbox '+postIndex,list.messages.some(msg=>msg.id===post.messageId))
  const runId=await currentRun(exclude),read=cli(['inbox','read',post.messageId,'--run',runId]);check('real runtime CLI read persists readAt '+postIndex,read.id===post.messageId&&!!read.readAt)
  const reply=cli(['inbox','reply',post.messageId,'--run',runId,'--text',text]);check('real runtime CLI reply has exact original inReplyTo '+postIndex,reply.inReplyTo===post.messageId&&reply.threadId===post.threadId)
  report.cli.push({rootMessageId:post.messageId,readRunId:runId,replyMessageId:reply.id,threadId:reply.threadId,mode:'actual compiled loopctl with actual fakePi runtime token; credentials not recorded'})
  await page.getByText(text,{exact:true}).waitFor();check('real SSE-updated reply once '+postIndex,await page.locator(`[data-inbox-message-id="${reply.id}"]`).count()===1)
  const after=(await inbox()).items,rootRecord=after.find(item=>item.message.id===post.messageId),replyRecord=after.find(item=>item.message.id===reply.id)
  check('actual GET read/reply links exact '+postIndex,rootRecord.runLinks.some(l=>l.role==='read'&&l.runId===runId)&&replyRecord.runLinks.some(l=>l.role==='reply'&&l.runId===runId))
 }
 await input.fill('WEB_REAL_INBOX_QUEUE');await send().click();await page.getByText('Inbox 已接收；读取、实际执行和回复以原记录为准。',{exact:true}).waitFor()
 check('one real queue POST/201, immutable receipt no run',report.posts.length===1&&report.posts[0].whenBusy==='queue'&&report.posts[0].status===201&&report.posts[0].receiptRunId===null)
 await readReply(0,'WEB_ACTUAL_INBOX_REPLY_ONE')
 await page.getByRole('combobox',{name:'忙时处理'}).selectOption('interrupt');await input.fill('WEB_REAL_INBOX_INTERRUPT');await send().click();await page.getByRole('button',{name:'读取原发送结果',exact:true}).waitFor()
 check('one real interrupt201 discarded receipt, no automatic repost',report.posts.length===2&&report.posts[1].whenBusy==='interrupt'&&report.posts[1].status===201)
 await input.fill('KEEP_NEW_DRAFT_AFTER_UNKNOWN');await page.getByRole('button',{name:'读取原发送结果',exact:true}).click();await page.getByText('Inbox 已接收；读取、实际执行和回复以原记录为准。',{exact:true}).waitFor();check('original action GET restores without POST and preserves edited draft',report.posts.length===2&&await input.inputValue()==='KEEP_NEW_DRAFT_AFTER_UNKNOWN')
 await readReply(1,'WEB_ACTUAL_INBOX_REPLY_TWO',m.initialRunId)
 const d=await detail(),node=d.nodes.find(n=>n.id===m.nodeRunId);check('ordinary Inbox does not answer Manager question/change attempt',node.question.status==='open'&&node.question.canAnswer===false&&node.attempt===1&&d.task.currentNodeRunId===m.nodeRunId)
 const timeline=await get(nodePath+`/runs/${m.initialRunId}/timeline`);check('real task tool timeline scope/actual tool event',timeline.taskId===m.taskId&&timeline.nodeRunId===m.nodeRunId&&timeline.run.id===m.initialRunId&&timeline.entries.some(e=>e.type==='tool'&&e.toolName==='bash'))
 const run=page.locator(`[data-task-run-id="${m.initialRunId}"]`);await run.getByRole('button',{name:/^运行记录/}).click();await run.getByText(m.initialRunId,{exact:true}).waitFor()
 for(const viewport of [{width:1440,height:1000},{width:390,height:844}]){await page.setViewportSize(viewport);await page.locator('[data-node-draft]').scrollIntoViewIfNeeded();check('no horizontal page overflow '+viewport.width,await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));await page.screenshot({path:new URL(`accepted-${viewport.width}.png`,out).pathname})}
 await page.reload();await page.getByText('WEB_ACTUAL_INBOX_REPLY_TWO',{exact:true}).waitFor();check('actual new snapshot/reload preserves exact Inbox records without new POST',report.posts.length===2&&await page.locator(`[data-inbox-message-id="${report.posts[0].messageId}"]`).count()===1)
 await input.fill('KEEP_ORIGINAL_NODE_DRAFT_ON_TERMINAL');await send().click();for(let i=0;i<100&&report.posts.length<3;i++)await page.waitForTimeout(50)
 check('real task terminal race409 singlePOST/no fallback',report.posts.length===3&&report.posts[2].status===409)
 await page.getByText(/原任务节点或运行条件已变化/).waitFor();check('terminal keeps old draft readonly',await input.inputValue()==='KEEP_ORIGINAL_NODE_DRAFT_ON_TERMINAL'&&await input.getAttribute('readonly')==='')
 await page.locator('[data-node-draft]').scrollIntoViewIfNeeded();await page.screenshot({path:new URL('terminal-390.png',out).pathname})
 const history=await inbox();check('historical Inbox/root/replies remain real after cancel',history.items.length===4)
 const recovered=await get(nodePath+'/inbox-actions/'+report.posts[1].actionId);check('historical action immutable original ID still readable',recovered.actionId===report.posts[1].actionId&&recovered.message.id===report.posts[1].messageId)
 check('actual App SSE response observed',report.forwarded.some(entry=>entry.path.endsWith(`/tasks/${m.taskId}/events`)&&entry.status===200))
 check('no browser product errors or external requests',!report.errors.length&&!report.console.length&&!report.denied.length)
 report.cleanup={browserClosed:true,contextClosed:true,viteStopped:true};writeFileSync(new URL('browser-results.json',out),JSON.stringify(report,null,2))
}catch(e){report.failure={type:e.name,message:e.message.replace(/https?:\/\/\S+/g,'[url]').slice(0,500),line:e.stack?.match(/task-inbox-app\.mjs:\d+:\d+/)?.[0]??null};if(page)await page.screenshot({path:new URL('browser-failure.png',out).pathname,fullPage:true}).catch(()=>{});writeFileSync(new URL('browser-failure.json',out),JSON.stringify(report,null,2));process.exitCode=1}
finally{closing=true;await page?.unrouteAll({behavior:'wait'});await context?.close();await browser?.close();if(preview&&preview.exitCode===null){preview.kill('SIGTERM');await new Promise(resolve=>{const timer=setTimeout(resolve,3000);preview.once('exit',()=>{clearTimeout(timer);resolve()})})}}
