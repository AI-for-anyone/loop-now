/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const m=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_CLI_VERIFICATION_MANIFEST,'utf8')),api=new URL(m.apiBase),web=new URL(m.webOrigin)
assert.equal(api.hostname,'127.0.0.1');assert.equal(api.pathname,'/api/v1');assert.equal(web.origin,'http://127.0.0.1:4320');assert.ok(!['8780','8781','5173',''].includes(api.port));assert.ok(!api.search&&!api.hash&&!api.username&&!api.password)
const root=fileURLToPath(new URL('../',import.meta.url)),out=m.evidenceDir
mkdirSync(out,{recursive:true})
const inputs={};for(const file of ['../api/openapi.yaml','src/api/schema.gen.ts','src/api/cli-verification.ts','src/features/project-credentials/CLIConnectionVerification.tsx','scripts/cli-verification-app.mjs'])inputs[file]=createHash('sha256').update(readFileSync(root+file)).digest('hex')
let browser,preview,context,forwarded=[],discard=false,replaceBeforePost=false
const checks=[],posts=[],controls=[],errors=[],denied=[],consoleKinds=[],safeResults=[]
const region=(page,cli)=>page.getByRole('region',{name:'连接验证 · Web '+cli})
const startPreview=()=>new Promise((resolve,reject)=>{preview=spawn(process.execPath,[root+'node_modules/vite/bin/vite.js','--host','127.0.0.1','--port',web.port,'--strictPort'],{cwd:root,env:{PATH:process.env.PATH},stdio:['ignore','pipe','pipe']});const timeout=setTimeout(()=>reject(new Error('temporary frontend not ready')),10000);preview.stdout.on('data',b=>{if(b.toString().includes(web.origin)){clearTimeout(timeout);resolve()}});preview.once('exit',()=>{clearTimeout(timeout);reject(new Error('temporary frontend exited'))})})
try{
 await startPreview();browser=await chromium.launch({headless:true});context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.route('**/*',route=>{const q=route.request(),u=new URL(q.url());const mutating=['POST','PUT','PATCH','DELETE'].includes(q.method());const accountPath=`/api/v1/projects/${m.projectId}/credential-accounts/`;if(![web.origin,api.origin,'http://127.0.0.1:8780'].includes(u.origin)||mutating&&(!u.pathname.startsWith(accountPath)||!u.pathname.includes('/verification-operations'))){denied.push({method:q.method(),path:u.pathname});return route.abort()}return route.fallback()})
 const page=await context.newPage();page.on('pageerror',()=>errors.push('pageerror'));page.on('console',event=>{if(event.type()==='error')consoleKinds.push('console.error; response status recorded separately, no raw body')})
 forwarded=await forwardTemporaryAPI(page,m.apiBase,web.origin)
 const get=async path=>{const r=await context.request.get(m.apiBase+path,{headers:{origin:web.origin},maxRetries:0,maxRedirects:0});assert.equal(r.status(),200);return r.json()}
 const base=id=>`/projects/${m.projectId}/credential-accounts/${id}/verification-operations`
 await page.route('**/api/v1/projects/*/credential-accounts/*/verification-operations',async route=>{
  const q=route.request();if(q.method()!=='POST')return route.fallback();const u=new URL(q.url()),id=u.pathname.split('/').at(-2),body=q.postDataJSON()
  assert.deepEqual(Object.keys(body).sort(),['expectedVersionId','operationId'])
  if(replaceBeforePost&&id===m.accounts.gh){replaceBeforePost=false;const account=await get(`/projects/${m.projectId}/credential-accounts/${id}`);const r=await context.request.put(m.apiBase+`/projects/${m.projectId}/credential-accounts/${id}/credential`,{headers:{origin:web.origin},data:{expectedRevision:account.revision,credential:{type:'cli',fields:{token:'synthetic-web-gh-B'}}},maxRetries:0,maxRedirects:0});assert.equal(r.status(),200);controls.push({method:'PUT',accountId:id,status:200,mode:'explicit TEST-only version replacement before held original POST'})}
  const response=await route.fetch({url:api.origin+u.pathname,headers:{...q.headers(),origin:web.origin,host:api.host},maxRetries:0,maxRedirects:0,timeout:30000})
  const data=await response.json();posts.push({accountId:id,...body,status:response.status(),operationIdReturned:data.id??null,mode:'actual temporary App route.fetch',version:response.headers()['loop-now-version']??null})
  if(discard&&id===m.accounts.gh){discard=false;assert.equal(response.status(),202);return route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:{code:'internal_error',message:'TEST transport discarded actual accepted receipt'}})})}
  return route.fulfill({response})
 })
 await page.goto(web.origin+`/p/${encodeURIComponent(m.projectName)}/settings`)
 await region(page,'gh').getByText('当前凭证版本尚未验证。').waitFor()
 assert.equal(posts.length,0)
 const initial=await get(`/projects/${m.projectId}/credential-accounts`);assert.equal(initial.accounts.length,3);assert.ok(initial.accounts.every(a=>a.status==='saved'))
 const initialVersions=Object.fromEntries(initial.accounts.map(a=>[a.cli,a.currentVersionId]))
 checks.push('actual settings GET and saved account cards do not POST probes; fixed current versions recorded')
 for(const cli of (m.slice==='install-state'?['gh','mysql']:['gh','mysql','psql'])){await region(page,cli).getByRole('button',{name:'测试连接',exact:true}).click();let terminal;for(let i=0;i<Math.ceil((m.installTimeoutMs+10000)/200);i++){terminal=await get(base(m.accounts[cli]));if(terminal.operation&&!['pending','running'].includes(terminal.operation.status))break;await page.waitForTimeout(200)}safeResults.push({cli,status:terminal.operation?.status,errorCode:terminal.operation?.result?.errorCode,toolVersion:terminal.operation?.toolVersion});assert.equal(terminal.operation?.status,'succeeded');await region(page,cli).getByText('本次验证通过').waitFor({timeout:10000});const state=await get(base(m.accounts[cli]));assert.equal(state.operation.status,'succeeded');assert.equal(state.operation.versionId,initialVersions[cli]);assert.equal(state.operation.probeKind,cli==='gh'?'github_user':'database_select1');assert.equal(state.operation.toolVersion,cli==='gh'?'2.100.0':cli==='mysql'?'8.4.9':'18.3');assert.ok(state.operation.result.verifiedAt);const version=cli==='gh'?'2.100.0':cli==='mysql'?'8.4.9':'18.3';await region(page,cli).locator('..').getByText(`工具准备：已安装 · ${cli} v${version}`,{exact:true}).waitFor({timeout:10000})}
 assert.equal(posts.length,m.slice==='install-state'?2:3);checks.push('selected actual HTTP202 workers persisted passes from fixed real native version checks and explicit offline probe children; real GET projected finite scope/time')
 for(const viewport of [{width:1440,height:1000},{width:390,height:1000}]){await page.setViewportSize(viewport);await page.getByRole('heading',{name:'CLI 账号',exact:true}).scrollIntoViewIfNeeded();assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false);await page.screenshot({path:out+`/passed-${viewport.width}.png`,fullPage:true})}
 if(m.slice!=='install-state'){
 discard=true;await region(page,'gh').getByRole('button',{name:'重新测试',exact:true}).click();for(let i=0;i<100&&posts.length<4;i++)await page.waitForTimeout(100);assert.equal(posts.length,4)
 for(let i=0;i<150;i++){const button=region(page,'gh').getByRole('button',{name:'重新测试',exact:true});if(await button.count()&&await button.isEnabled())break;await page.waitForTimeout(100)}await region(page,'gh').getByText('本次验证通过').waitFor({timeout:30000})
 const unknown=posts.at(-1);assert.equal(posts.filter(p=>p.operationId===unknown.operationId).length,1);const recovered=await get(base(m.accounts.gh)+`/${unknown.operationId}`);assert.equal(recovered.status,'succeeded');checks.push('actual accepted202 lost only at browser transport; same original operation recovered by GET, no automatic second POST')
 writeFileSync(m.controlDir+'/mysql.mode','fail');await region(page,'mysql').getByRole('button',{name:'重新测试',exact:true}).click();await region(page,'mysql').getByText('本次验证失败').waitFor({timeout:30000});assert.equal((await get(`/projects/${m.projectId}/credential-accounts/${m.accounts.mysql}`)).status,'saved');checks.push('actual worker failed parse result leaves saved account intact, no auto retry')
 writeFileSync(m.controlDir+'/psql.mode','hold');await region(page,'psql').getByRole('button',{name:'重新测试',exact:true}).click();await region(page,'psql').getByText('正在执行固定在线检查').waitFor({timeout:30000});await region(page,'psql').getByRole('button',{name:'取消本次验证',exact:true}).click();await region(page,'psql').getByText('本次验证已取消').waitFor({timeout:30000});const cancelled=await get(base(m.accounts.psql));assert.equal(cancelled.operation.status,'cancelled');assert.equal(cancelled.operation.result.outcome,'cancelled');checks.push('actual cancellation committed and stopped private fixture child; GET terminal projection')
 replaceBeforePost=true;await region(page,'gh').getByRole('button',{name:'重新测试',exact:true}).click();for(let i=0;i<100&&posts.length<7;i++)await page.waitForTimeout(100);assert.equal(posts.length,7);assert.equal(posts.at(-1).status,412);assert.equal(posts.at(-1).expectedVersionId,initialVersions.gh)
 await region(page,'gh').getByText('当前凭证版本尚未验证。').waitFor({timeout:30000});assert.equal(await region(page,'gh').getByText('本次验证通过').count(),0);const old=await get(base(m.accounts.gh)+`/${unknown.operationId}`);assert.equal(old.isCurrentVersion,false);assert.equal(old.status,'succeeded');checks.push('real TEST replacement races old-version POST412; latest V2 untested, old successful V1 readable only as noncurrent')
 await region(page,'gh').scrollIntoViewIfNeeded();await page.screenshot({path:out+'/terminal-version-390.png',fullPage:true})
 }
 assert.ok(posts.every(p=>p.status===202||p.status===412));assert.deepEqual(errors,[]);assert.deepEqual(denied,[])
 writeFileSync(out+'/browser-results.json',JSON.stringify({boundary:'actual React -> actual temporary App/TEST PG/Redis worker; native --version + explicit probe command child fixture; no native network/provider acceptance',checks,posts,controls,forwarded,errors,denied,consoleKinds,safeResults,inputs},null,2))
}catch(error){writeFileSync(out+'/browser-failure.json',JSON.stringify({errorType:error.name,sourceLine:error.stack?.match(/cli-verification-app\.mjs:\d+:\d+/)?.[0]??null,checks,posts,controls,forwarded,errors,denied,consoleKinds,safeResults,inputs},null,2));process.exitCode=1}
finally{await context?.close();await browser?.close();if(preview&&preview.exitCode===null){preview.kill('SIGTERM');await new Promise(resolve=>{const timer=setTimeout(resolve,3000);preview.once('exit',()=>{clearTimeout(timer);resolve()})})}}
