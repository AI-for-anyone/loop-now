/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'
const m=JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_DB_MANIFEST,'utf8')),api=new URL(m.apiBase),web=new URL(m.webOrigin)
assert.equal(m.slice,'google-view');assert.equal(api.hostname,'127.0.0.1');assert.equal(api.pathname,'/api/v1');assert.ok(!['5173','8780','8781',''].includes(api.port));assert.equal(web.origin,'http://127.0.0.1:4318')
const out=new URL('../test-results/add-cloud-database-cli-templates/app-google-view/',import.meta.url);mkdirSync(out,{recursive:true})
const root=fileURLToPath(new URL('../',import.meta.url)),errors=[],denied=[];let browser,preview,forwarded=[]
try{
 await new Promise((resolve,reject)=>{preview=spawn(process.execPath,[root+'node_modules/vite/bin/vite.js','--host','127.0.0.1','--port','4318','--strictPort'],{cwd:root,env:{PATH:process.env.PATH},stdio:['ignore','pipe','pipe']});const timer=setTimeout(()=>reject(new Error('temporary frontend readiness')),10000);preview.stdout.on('data',b=>{if(b.toString().includes(web.origin)){clearTimeout(timer);resolve()}})})
 browser=await chromium.launch({headless:true});const context=await browser.newContext({viewport:{width:1440,height:1000},serviceWorkers:'block'})
 await context.route('**/*',r=>{const q=r.request(),u=new URL(q.url());if(![web.origin,api.origin,'http://127.0.0.1:8780'].includes(u.origin)||!['GET','HEAD','OPTIONS'].includes(q.method())){denied.push({method:q.method(),path:u.pathname});return r.abort()}return r.fallback()})
 const page=await context.newPage();page.on('pageerror',()=>errors.push('pageerror'));forwarded=await forwardTemporaryAPI(page,m.apiBase,web.origin)
 await page.goto(web.origin+`/p/${encodeURIComponent(m.projectName)}/settings`)
 const region=page.getByRole('region',{name:'CLI 账号',exact:true}),account=region.getByRole('listitem').filter({has:page.getByRole('heading',{name:'合成Google已保存账号',exact:true})})
 await account.getByRole('button',{name:'重新登录 Google',exact:true}).waitFor();assert.ok((await account.innerText()).includes('个人 Google 浏览器登录'))
 await region.getByLabel('添加 CLI 账号 · 工具').selectOption('gcloud')
 for(const viewport of [{width:1440,height:1000},{width:390,height:844}]){await page.setViewportSize(viewport);await region.getByRole('heading',{name:'CLI 账号',exact:true}).scrollIntoViewIfNeeded();await page.waitForTimeout(100);await page.screenshot({path:new URL(`google-metadata-${viewport.width}.png`,out).pathname});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false)}
 assert.deepEqual(errors,[]);assert.deepEqual(denied,[]);assert.ok(forwarded.every(r=>r.method==='GET'))
 writeFileSync(new URL('browser-results.json',out),JSON.stringify({boundary:'actual React readonly GET -> isolated App synthetic metadata fixture; no login lifecycle/auth POST/provider',accountId:m.accountId,forwarded,errors,denied,viewports:['1440x1000','390x844']},null,2))
}catch(e){writeFileSync(new URL('browser-failure.json',out),JSON.stringify({errorType:e.name,sourceLine:e.stack?.match(/google-metadata-app\.mjs:\d+:\d+/)?.[0]??null,forwarded,errors,denied},null,2));process.exitCode=1}
finally{await browser?.close();if(preview&&preview.exitCode===null){preview.kill('SIGTERM');await new Promise(resolve=>{const timer=setTimeout(resolve,3000);preview.once('exit',()=>{clearTimeout(timer);resolve()})})}}
