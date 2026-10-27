// Explicit platform extension. The parent captures and validates this value;
// it is not a project/host setting and never reads credential material.
const allowed = new Set(['default', 'priority', 'ultrafast']);
function fatal() {
  process.stderr.write('Codex service tier configuration unavailable\n');
  // Pi reports then swallows event-handler exceptions. Exit instead of allowing
  // an unmodified request through when this trusted adapter cannot apply it.
  process.exit(78);
}
export default function install(pi) {
  const tier = process.env.LOOP_NOW_CODEX_SERVICE_TIER;
  if (!allowed.has(tier)) fatal();
  pi.on('before_provider_request', (event, ctx) => {
    if (!ctx.model || typeof ctx.model.provider !== 'string') fatal();
    if (ctx.model.provider !== 'openai-codex') return;
    const payload = event.payload;
    if (!payload || typeof payload !== 'object' || Array.isArray(payload)) fatal();
    return { ...payload, service_tier: tier };
  });
}
