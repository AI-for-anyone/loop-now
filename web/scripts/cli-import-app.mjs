/* global document, window, innerWidth */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'

const m=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_CLI_IMPORT_MANIFEST,'utf8')),api=new URL(m.apiBase),web=new URL(m.webOrigin)
assert.equal(api.hostname,'127.0.0.1');assert.equal(api.pathname,'/api/v1');assert.equal(web.origin,'http://127.0.0.1:4322');assert.ok(!['8780','8781','5173',''].includes(api.port));assert.ok(!api.search&&!api.hash&&!api.username&&!api.password)
const root=fileURLToPath(new URL('../',import.meta.url)),out=m.evidenceDir;mkdirSync(out,{recursive:true})
const inputs={};for(const file of ['../api/openapi.yaml','src/api/schema.gen.ts','src/features/project-credentials/LocalCLIImport.tsx','scripts/cli-import-app.mjs','../internal/server/app/web_cli_import_integration_test.go'])inputs[file]=createHash('sha256').update(readFileSync(root+file)).digest('hex')
let browser,preview,context,releaseRead,blocked=false,discard=true
const readsReady=new Promise(resolve=>{releaseRead=resolve}),requests=[],starts=[],scans=[],states=[],errors=[],denied=[],consoleKinds=[],checks={}
const save=(name,value)=>writeFileSync(out+'/'+name,JSON.stringify(value,null,2)+'\n',{mode:0o600})
const waitPreview=()=>new Promise((resolve,reject)=>{preview=spawn(process.execPath,[root+'node_modules/vite/bin/vite.js','--host','127.0.0.1','--port','4322','--strictPort'],{cwd:root,env:{PATH:process.env.PATH},stdio:['ignore','pipe','pipe']});const timer=setTimeout(()=>reject(Error('temporary frontend not ready')),10000);preview.stdout.on('data',b=>{if(b.toString().includes(web.origin)){clearTimeout(timer);resolve()}});preview.once('exit',()=>{clearTimeout(timer);reject(Error('temporary frontend exited'))})})
try{
 await waitPreview();browser=await chromium.launch({headless:true});context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.route('**/*',async route=>{
  const q=route.request(),u=new URL(q.url()),method=q.method()
  if(u.origin===web.origin&&u.pathname.startsWith('/p/'))return route.fulfill({contentType:'text/html',body:'<!doctype html><html lang="zh-CN"><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div></body></html>'})
  if(u.origin===web.origin&&!u.pathname.startsWith('/api/'))return route.continue()
  if(u.origin!=='http://127.0.0.1:8780'||!u.pathname.startsWith('/api/v1/')){denied.push({method,path:u.pathname});return route.abort()}
  if(blocked&&method==='GET'&&u.pathname.includes('/imports/'))await readsReady
  const dest=new URL(u.pathname+u.search,api.origin),headers={...q.headers(),origin:web.origin,host:api.host}
  const resp=await route.fetch({url:dest.href,headers,maxRedirects:0,maxRetries:0,timeout:15000})
  const entry={method,path:u.pathname,status:resp.status(),mode:'route.fetch actual isolated App'};requests.push(entry)
  const raw=await resp.text();assert.ok(!raw.includes('SYNTHETIC_WEB_IMPORT_PRIVATE'));assert.ok(!raw.includes('/.config/gh/'))
  if(method==='POST'&&u.pathname.endsWith('/scans')){assert.deepEqual(q.postDataJSON(),{});assert.equal(resp.status(),201);scans.push(JSON.parse(raw))}
  if(method==='POST'&&u.pathname.endsWith('/imports')){const body=q.postDataJSON();assert.deepEqual(Object.keys(body).sort(),['candidateIds','expectedCredentialsRevision','operationId','scanId','supplements']);assert.deepEqual(body.supplements,[]);assert.equal(body.candidateIds.length,2);assert.equal(resp.status(),202);starts.push(body);if(discard){discard=false;return route.fulfill({status:502,contentType:'application/json',body:JSON.stringify({error:{code:'internal_error',message:'test transport discarded accepted receipt'}})})}}
  if(method==='GET'&&u.pathname.includes('/imports/')&&resp.status()===200)states.push(JSON.parse(raw).operation)
  return route.fulfill({response:resp})
 })
 const p=await context.newPage();p.on('pageerror',()=>errors.push('pageerror'));p.on('console',v=>{if(v.type()==='error')consoleKinds.push({kind:'error',category:v.text().includes('[vite]')?'vite-dev-transport':/Failed to load resource/.test(v.text())?'resource-load':'other',status:v.text().match(/status of (\d+)/)?.[1]??null,location:(()=>{try{const u=new URL(v.location().url);return u.origin+u.pathname}catch{return null}})()})})
 const mount=async()=>p.evaluate(async project=>{
  const runtime=(await import('/@react-refresh')).default;runtime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};window.$RefreshSig$=()=>type=>type;window.__vite_plugin_react_preamble_installed__=true;await import('/src/index.css')
  const source=(await Promise.all(['/src/main.tsx','/src/api/queries.ts','/src/features/project-credentials/LocalCLIImport.tsx'].map(async path=>(await fetch(path)).text()))).join('\n'),deps=[...source.matchAll(/["'](\/node_modules\/\.vite\/deps\/[^"']+)["']/g)].map(v=>v[1]),dep=n=>deps.find(path=>path.includes('/'+n+'.js?'))
  const React=(await import(dep('react'))).default,{createRoot}=(await import(dep('react-dom_client'))).default,{QueryClient,QueryClientProvider}=await import(dep('@tanstack_react-query')),{createBrowserRouter,RouterProvider}=await import(dep('react-router')),{ProjectAccounts}=await import('/src/features/project-credentials/ProjectAccounts.tsx'),{ScopeContext}=await import('/src/features/projects/scope-context.ts'),{DraftNavigationGuard}=await import('/src/features/prompts/DraftNavigationGuard.tsx'),h=React.createElement
  const router=createBrowserRouter([{path:'/p/:name/settings',element:h(ScopeContext.Provider,{value:{kind:'project',project}},h(DraftNavigationGuard,null,h('main',{className:'p-4'},h(ProjectAccounts,{projectId:project.id,purpose:'cli'}))))}])
  createRoot(document.getElementById('root')).render(h(QueryClientProvider,{client:new QueryClient({defaultOptions:{queries:{retry:false}}})},h(RouterProvider,{router})))
 },{id:m.projectId,name:m.projectName})
 await p.goto(web.origin+'/p/web-import/settings');await mount();await p.getByRole('button',{name:'从本机导入',exact:true}).waitFor();assert.equal(scans.length,0);assert.equal(starts.length,0)
 await p.getByRole('button',{name:'从本机导入',exact:true}).click();assert.equal(scans.length,0)
 const scan=()=>p.getByRole('button',{name:/^(扫描本机候选|重新扫描)$/})
 const selectBoth=async()=>{await p.getByRole('checkbox').nth(1).waitFor();assert.equal(await p.getByRole('checkbox').count(),2);for(const box of await p.getByRole('checkbox').all()){assert.equal(await box.isChecked(),false);assert.equal(await box.isDisabled(),false);await box.check()}}
 const confirm=async()=>{await p.getByRole('button',{name:'核对导入',exact:true}).click();await p.getByRole('button',{name:'确认导入保存',exact:true}).click()}
 await scan().click();await selectBoth();blocked=true;await confirm()
 await p.getByText('提交结果待确认。只读取原操作，不重新创建批次或扫描来源。',{exact:true}).waitFor();assert.equal(starts.length,1);checks.unknownOriginalId=new URL(p.url()).searchParams.get('cliImport')===starts[0].operationId
 await p.screenshot({path:out+'/unknown1440.png',fullPage:true});blocked=false;releaseRead()
 await p.getByRole('button',{name:'读取原操作结果',exact:true}).click();await p.getByText('整批导入已保存',{exact:true}).waitFor();assert.equal(starts.length,1)
 const id=starts[0].operationId;await p.reload();await mount();await p.getByText('整批导入已保存',{exact:true}).waitFor();assert.equal(new URL(p.url()).searchParams.get('cliImport'),id);assert.equal(starts.length,1);assert.equal(scans.length,1)
 for(const width of [1440,390]){await p.setViewportSize({width,height:1000});checks[width]=await p.evaluate(()=>({width:innerWidth,scrollWidth:document.documentElement.scrollWidth}));assert.ok(checks[width].scrollWidth<=width);await p.screenshot({path:out+'/saved'+width+'.png',fullPage:true})}
 await scan().click();await selectBoth();assert.ok(scans[1].candidates.every(v=>v.previouslyImported));await confirm();await p.getByText('整批导入已保存',{exact:true}).waitFor()
 await p.getByRole('button',{name:'读取原操作结果',exact:true}).click();await p.getByText('此前已导入，已有账号保持',{exact:true}).first().waitFor()
 assert.equal(starts.length,2);assert.equal(scans.length,2);assert.notEqual(starts[0].operationId,starts[1].operationId)
 const final=states.findLast(v=>v.id===starts[1].operationId&&v.status==='succeeded');assert.ok(final);assert.ok(final.items.every(v=>v.status==='already_imported'))
 const first=states.find(v=>v.id===starts[0].operationId&&v.status==='succeeded');assert.ok(first);assert.ok(first.items.every(v=>v.status==='imported'));assert.deepEqual(final.items.map(v=>v.accountId).sort(),first.items.map(v=>v.accountId).sort())
 assert.equal(requests.filter(v=>v.method==='POST'&&!v.path.endsWith('/scans')&&!v.path.endsWith('/imports')).length,0);assert.equal(errors.length,0);assert.equal(denied.length,0)
 checks.singlePOSTPerOperation=true;checks.reloadOnlyGET=true;checks.atomicTwoSaved=true;checks.alreadyImportedSameIDs=true;checks.noAutomaticVerification=true
 save('browser-results.json',{boundary:'actual React with actual App responses; one accepted 202 discarded at browser transport, no invented server success; synthetic source only',inputs,checks,requests,starts,operationStates:states,errors,denied,consoleKinds});console.log('isolated React import slice passed')
}catch(e){save('browser-failure.json',{failure:String(e),inputs,checks,requests,starts,errors,denied,consoleKinds});throw e}
finally{releaseRead();await context?.close();await browser?.close();if(preview&&preview.exitCode===null){await new Promise(resolve=>{preview.once('exit',resolve);preview.kill('SIGTERM')})}}
