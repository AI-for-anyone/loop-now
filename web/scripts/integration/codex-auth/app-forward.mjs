// Test-only browser transport: actual React requests reach the actual isolated App.
// This does not invent response bodies or add any server route.
export async function forwardTemporaryAPI(page, apiBase, webOrigin = 'http://localhost:5173') {
  const target = new URL(apiBase)
  if (target.protocol !== 'http:' || target.hostname !== '127.0.0.1' || !target.port || ['8780', '8781', '5173'].includes(target.port) || target.username || target.password || target.search || target.hash || target.pathname.replace(/\/$/, '') !== '/api/v1') {
    throw new Error('Expected an isolated temporary loopback App API, never an existing service')
  }
  const forwarded = []
  page.on('response', response => {
    const request=response.request()
    if ((request.headers().accept ?? '').includes('text/event-stream')) {
      forwarded.push({ method:request.method(),path:new URL(response.url()).pathname,status:response.status(),mode:'route.continue actual temporary App SSE response' })
    }
  })
  await page.route('http://127.0.0.1:8780/api/v1/**', async route => {
    const request = route.request(), original = new URL(request.url())
    const destination = new URL(original.pathname + original.search, target.origin)
    const headers = { ...request.headers(), origin: webOrigin, host: target.host }
    // SSE is an actual open response; route.fetch buffers it indefinitely.
    // Continue directly to the same temporary App instead of mocking the stream.
    if ((request.headers().accept ?? '').includes('text/event-stream')) {
      await route.continue({ url:destination.href,headers });return
    }
    try {
      const response = await route.fetch({ url: destination.href, headers, timeout: 15000, maxRedirects: 0, maxRetries: 0 })
      const record={ method: request.method(), path: original.pathname, status: response.status(), requestId: response.headers()['loop-now-request-id'] ?? null, mode: 'route.fetch actual temporary App response' }
      if(original.pathname.endsWith('/model-auth')&&response.status()===200){const value=await response.json();record.credentialVersionId=value.credential?.versionId??null;record.operationStatus=value.operation?.status??null}
      forwarded.push(record)
      await route.fulfill({ response })
    } catch (error) {
      forwarded.push({ method: request.method(), path: original.pathname, status: 0, errorType: error.name, mode: 'actual temporary App transport failure' })
      await route.abort()
    }
  })
  return forwarded
}
