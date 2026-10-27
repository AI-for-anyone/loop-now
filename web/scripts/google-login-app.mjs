/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_DB_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(manifest.slice, 'google'); assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.protocol, 'http:')
assert.equal(api.pathname, '/api/v1'); assert.ok(!['8780', '8781', '5173', ''].includes(api.port))
assert.equal(web.origin, 'http://127.0.0.1:4318'); assert.ok(!api.username && !api.password && !api.search && !api.hash)
const out = new URL('../test-results/add-cloud-database-cli-templates/app-google/', import.meta.url)
mkdirSync(out, { recursive: true })
const checks = [], errors = [], denied = [], ids = {}, screenshots = []
let browser, preview, forwarded = []
const issuer = [], starts = [], cancels = []
const root = fileURLToPath(new URL('../', import.meta.url))
const startPreview = () => new Promise((resolve, reject) => {
  preview = spawn(process.execPath, [root + 'node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', web.port, '--strictPort'], { cwd: root, env: { PATH: process.env.PATH }, stdio: ['ignore', 'pipe', 'pipe'] })
  const timeout = setTimeout(() => reject(new Error('temporary frontend readiness timeout')), 10000)
  preview.stdout.on('data', buffer => { if (buffer.toString().includes(web.origin)) { clearTimeout(timeout); resolve() } })
  preview.once('exit', () => { clearTimeout(timeout); reject(new Error('temporary frontend exited')) })
  preview.once('error', () => { clearTimeout(timeout); reject(new Error('temporary frontend spawn failed')) })
})
try {
  await startPreview(); browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await context.route('**/*', route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === 'https://accounts.google.com') { issuer.push({ origin: url.origin, path: url.pathname }); return route.fulfill({ contentType: 'text/html', body: '<title>Offline injected Google issuer</title><p>Offline navigation fixture</p>' }) }
    const writeAllowed = /\/(credential-accounts(?:\/[^/]+(?:\/credential)?)?|credential-selections|cli-auth\/operations(?:\/[^/]+\/cancel)?)$/.test(url.pathname)
    if (![web.origin, api.origin, 'http://127.0.0.1:8780'].includes(url.origin) || (request.method() !== 'GET' && !writeAllowed)) {
      denied.push({ method: request.method(), path: url.pathname }); return route.abort()
    }
    return route.fallback()
  })
  const page = await context.newPage()
  page.on('pageerror', () => errors.push('pageerror'))
  forwarded = await forwardTemporaryAPI(page, manifest.apiBase, web.origin)
  const get = async path => {
    const response = await context.request.get(manifest.apiBase + path, { headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), 200); return response.json()
  }
  const base = `/p/${encodeURIComponent(manifest.projectName)}`
  const region = () => page.getByRole('region', { name: 'CLI 账号', exact: true })
  const row = name => region().getByRole('listitem').filter({ has: page.getByRole('heading', { name, exact: true }) })
  const create = async (name, json, capture = false) => {
    await page.goto(web.origin + base + '/settings')
    await region().getByLabel('添加 CLI 账号 · 工具').selectOption('gcloud')
    await region().getByRole('button', { name: '录入服务账号 JSON', exact: true }).click()
    await region().getByLabel('账号名称', { exact: true }).fill(name)
    const input = region().getByLabel('服务账号 JSON', { exact: false })
    assert.equal(await input.evaluate(element => element.tagName), 'TEXTAREA')
    await input.fill(json)
    assert.equal(await input.evaluate(element => window.getComputedStyle(element).color), 'rgba(0, 0, 0, 0)')
    await region().getByLabel('默认 GCP 项目（可选）', { exact: true }).fill('synthetic-target')
    if (capture) for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport); await region().screenshot({ path: new URL(`sa-${viewport.width}.png`, out).pathname })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
      screenshots.push({ ...viewport, boundary: 'CLI region may exceed viewport height; all private characters masked' })
    }
    await region().getByRole('button', { name: '保存', exact: true }).click()
    await region().getByRole('heading', { name, exact: true }).waitFor()
    await input.waitFor({ state: 'hidden' })
    const account = (await get(`/projects/${manifest.projectId}/credential-accounts`)).accounts.find(item => item.name === name)
    assert.ok(account); assert.equal(account.authMethod, 'service_account')
    assert.ok(!JSON.stringify(account).includes('PRIVATE KEY')); return account
  }
  const a = await create('合成Google原账号', manifest.sa1, true); ids.a = a.id
  const active = async target => (await get(`/projects/${manifest.projectId}/cli-auth/operations`)).operations.find(op => op.targetAccountId === target && ['pending','awaiting_authorization'].includes(op.status))
  const state = async id => get(`/projects/${manifest.projectId}/cli-auth/operations/${id}`)
  const waitStatus = async (id, expected) => { for (let i=0;i<100;i++) { const value=await state(id); if(value.operation.status===expected)return value; await page.waitForTimeout(100) } throw new Error('operation did not reach expected safe status') }
  const control = (id, action) => writeFileSync(manifest.controlDir+'/'+id+'.json', JSON.stringify({ action }), { mode:0o600 })
  const startExisting = async () => {
    const popupPromise = page.waitForEvent('popup')
    await row(a.name).getByRole('button', { name: '重新登录 Google', exact:true }).click()
    const popup=await popupPromise
    let op
    for(let i=0;i<50;i++){op=await active(a.id);if(op)break;await page.waitForTimeout(100)}
    assert.ok(op); starts.push(op.id)
    // Controlled visibility models the actual popup putting the opener behind
    // it. Reads still go to the real App; onURL is delayed by the injected helper.
    await page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{get:()=> 'hidden',configurable:true});document.dispatchEvent(new Event('visibilitychange'))})
    await popup.waitForURL(url=>url.origin==='https://accounts.google.com',{timeout:15000})
    assert.equal(await popup.evaluate(()=>window.opener===null),true)
    await page.evaluate(()=>{Object.defineProperty(document,'visibilityState',{get:()=> 'visible',configurable:true});document.dispatchEvent(new Event('visibilitychange'))})
    await popup.close()
    return op
  }
  const failed=await startExisting();control(failed.id,'fail');await waitStatus(failed.id,'failed')
  await page.reload();await row(a.name).getByRole('button',{name:'重新登录 Google',exact:true}).waitFor()
  const unchanged=await get(`/projects/${manifest.projectId}/credential-accounts/${a.id}`)
  assert.equal(unchanged.currentVersionId,a.currentVersionId);assert.equal(unchanged.authMethod,'service_account')
  checks.push('real start/get/list with delayed injected onURL; controlled hidden opener keeps polling and popup leaves blank; failed re-login retains original SA account/version')
  const success=await startExisting()
  await page.reload();await row(a.name).getByRole('link',{name:'打开授权页',exact:true}).waitFor()
  assert.equal((await active(a.id)).id,success.id)
  const nativePromise=page.waitForEvent('popup');await row(a.name).getByRole('link',{name:'打开授权页',exact:true}).click();const native=await nativePromise
  await native.waitForURL(url=>url.origin==='https://accounts.google.com',{timeout:10000});assert.equal(await native.evaluate(()=>window.opener===null),true);await native.close()
  control(success.id,'succeed');const complete=await waitStatus(success.id,'succeeded')
  assert.equal(complete.operation.authorizationUrl,null);assert.equal(complete.account.id,a.id);assert.equal(complete.account.authMethod,'oauth');assert.notEqual(complete.account.currentVersionId,a.currentVersionId)
  await page.reload();await row(a.name).getByRole('button',{name:'重新登录 Google',exact:true}).waitFor()
  await row(a.name).filter({ hasText: '个人 Google 浏览器登录' }).waitFor()
  checks.push('reload recovers same active ID without POST; native continue opens intercepted Google issuer; success uses same account/new version and terminal authorization URL cleared')
  await region().getByLabel('添加 CLI 账号 · 工具').selectOption('gcloud')
  await region().getByLabel('新 Google 账号名称',{exact:true}).fill('合成Google取消操作')
  const newPopupPromise=page.waitForEvent('popup');await region().getByRole('button',{name:'登录 Google',exact:true}).click();const newPopup=await newPopupPromise
  let cancelOp;for(let i=0;i<50;i++){cancelOp=await active(null);if(cancelOp)break;await page.waitForTimeout(100)}assert.ok(cancelOp);starts.push(cancelOp.id)
  await region().getByRole('button',{name:'取消登录',exact:true}).click();await waitStatus(cancelOp.id,'cancelled');cancels.push(cancelOp.id)
  if (!newPopup.isClosed()) await newPopup.waitForEvent('close').catch(()=>{})
  await region().getByLabel('新 Google 账号名称',{exact:true}).waitFor();assert.equal(await region().getByLabel('新 Google 账号名称',{exact:true}).isEnabled(),true)
  const final=await get(`/projects/${manifest.projectId}/credential-accounts/${a.id}`);assert.equal(final.currentVersionId,complete.account.currentVersionId)
  const all=(await get(`/projects/${manifest.projectId}/credential-accounts`)).accounts;assert.equal(all.length,1)
  checks.push('actual cancel ends only its new-target operation; original successful account preserved, name editable, no phantom account')
  for(const viewport of [{width:1440,height:1000},{width:390,height:844}]){await page.setViewportSize(viewport);await region().screenshot({path:new URL(`google-${viewport.width}.png`,out).pathname});assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>window.innerWidth),false)}
  assert.deepEqual(errors,[]);assert.deepEqual(denied,[]);assert.equal(issuer.length,3)
  const posts=forwarded.filter(item=>item.method==='POST'&&item.path.endsWith('/cli-auth/operations'))
  assert.equal(posts.length,3);assert.ok(posts.every(item=>item.status===202));assert.equal(starts.length,3);assert.equal(cancels.length,1)
  assert.ok(!forwarded.some(item=>item.method!=='GET'&&/model-auth|messages/.test(item.path)))
  writeFileSync(new URL('browser-results.json',out),JSON.stringify({boundary:'actual React -> forwarded isolated App/TEST PG; App-injected offline Google helper and intercepted provider page only; no SDK/callback/provider/model',checks,ids,starts,cancels,issuer,forwarded,errors,denied,screenshots},null,2))
} catch (error) {
  // No request bodies, private JSON or provider content in the audit.
  writeFileSync(new URL('browser-failure.json', out), JSON.stringify({ errorType: error.name, message: 'sanitized Google browser assertion failed; see passed checks and safe forwarded statuses', sourceLine: error.stack?.match(/google-login-app\.mjs:\d+:\d+/)?.[0] ?? null, starts, cancels, issuer, checks, ids, forwarded, errors, denied }, null, 2)); process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) { preview.kill('SIGTERM'); await new Promise(resolve => { const timeout = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timeout); resolve() }) }) }
}
