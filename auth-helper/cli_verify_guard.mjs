// Trusted Node24 lifecycle guard. Protocol: guard-integration.md, protocol 1.
// Commands arrive only over the parent's private fd3, never argv/environment.
import fs from 'node:fs';
import path from 'node:path';
import { Socket } from 'node:net';
import { spawn } from 'node:child_process';
import { TextDecoder } from 'node:util';

const FRAME_LIMIT = 256 * 1024;
const OUTPUT_LIMIT = 64 * 1024;
const RESULT_LIMIT = 4 * 1024;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const NIL = '00000000-0000-0000-0000-000000000000';
const fields = ['protocol', 'type', 'executable', 'cwd', 'home', 'args', 'env',
  'deadlineUnixMs', 'termGraceMs', 'instanceId', 'operationId', 'leasePath'];
const decoder = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });

const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const exact = (value, keys) => object(value) && Object.keys(value).length === keys.length && keys.every(k => Object.hasOwn(value, k));
const text = value => typeof value === 'string' && !value.includes('\0');
const absolute = value => text(value) && path.isAbsolute(value) && path.resolve(value) === value;
const uuid = value => typeof value === 'string' && UUID.test(value) && value !== NIL;

// JSON.parse alone permits duplicate keys. Both command and owner records must
// have one unambiguous value per key, including escaped spelling equivalents.
function strictJSON(raw) {
  const source = decoder.decode(raw);
  const result = JSON.parse(source);
  const stack = [];
  for (let i = 0; i < source.length; i++) {
    const ch = source[i];
    if (ch === '{' || ch === '[') stack.push(ch === '{' ? new Set() : null);
    else if (ch === '}' || ch === ']') stack.pop();
    else if (ch === '"') {
      const start = i++;
      for (; i < source.length; i++) {
        if (source[i] === '\\') i++;
        else if (source[i] === '"') break;
      }
      let next = i + 1;
      while (/\s/.test(source[next] ?? '') && next < source.length) next++;
      if (source[next] === ':') {
        const seen = stack.at(-1);
        const key = JSON.parse(source.slice(start, i + 1));
        if (!(seen instanceof Set) || seen.has(key)) throw new Error();
        seen.add(key);
      }
    }
  }
  return result;
}

function same(a, b) { return a.dev === b.dev && a.ino === b.ino; }
function owned(file, kind, mode) {
  const s = fs.lstatSync(file, { bigint: true });
  if (s.isSymbolicLink() || s.uid !== BigInt(process.getuid()) || (mode !== undefined && (s.mode & 0o777n) !== BigInt(mode))
    || (kind === 'directory' ? !s.isDirectory() : !s.isFile())) throw new Error();
  return s;
}

function identity(spec, previous) {
  const root = path.dirname(path.dirname(spec.leasePath));
  if (path.basename(path.dirname(spec.leasePath)) !== '.cli-verification-locks'
    || spec.leasePath !== path.join(root, '.cli-verification-locks', `${spec.operationId}.lock`)
    || spec.home !== path.join(root, '.cli-verification', spec.instanceId, spec.operationId)) throw new Error();
  // WorkRoot is pre-existing platform storage, not a new private directory.
  // Still require its identity/ownership; 0700 applies to our private layers.
  owned(root, 'directory');
  for (const dir of [path.join(root, '.cli-verification'),
    path.join(root, '.cli-verification', spec.instanceId), path.dirname(spec.leasePath)]) {
    owned(dir, 'directory', 0o700);
  }
  const home = owned(spec.home, 'directory', 0o700);
  const lease = owned(spec.leasePath, 'file', 0o600);
  const inherited = fs.fstatSync(5, { bigint: true });
  if (!inherited.isFile() || inherited.uid !== BigInt(process.getuid())
    || (inherited.mode & 0o777n) !== 0o600n || !same(lease, inherited)) throw new Error();
  const ownerPath = path.join(spec.home, '.owner');
  const owner = owned(ownerPath, 'file', 0o600);
  if (owner.size > 4096n) throw new Error();
  const fd = fs.openSync(ownerPath, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  let marker;
  try {
    if (!same(owner, fs.fstatSync(fd, { bigint: true }))) throw new Error();
    marker = strictJSON(fs.readFileSync(fd));
  } finally { fs.closeSync(fd); }
  if (!exact(marker, ['protocol', 'instanceId', 'operationId']) || marker.protocol !== 1
    || marker.instanceId !== spec.instanceId || marker.operationId !== spec.operationId) throw new Error();
  if (previous && (!same(previous.home, home) || !same(previous.owner, owner) || !same(previous.lease, lease))) throw new Error();
  return { home, owner, lease };
}

function validate(spec) {
  if (!exact(spec, fields) || spec.protocol !== 1 || spec.type !== 'command'
    || !absolute(spec.executable) || !absolute(spec.cwd) || !absolute(spec.home) || !absolute(spec.leasePath)
    || !Array.isArray(spec.args) || !spec.args.every(text) || !object(spec.env)
    || !Object.entries(spec.env).every(([key, value]) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(key) && text(value))
    || !Number.isSafeInteger(spec.deadlineUnixMs) || spec.deadlineUnixMs <= Date.now()
    || !Number.isInteger(spec.termGraceMs) || spec.termGraceMs < 1 || spec.termGraceMs > 5000
    || !uuid(spec.instanceId) || !uuid(spec.operationId)) throw new Error();
  // HOME is an explicit scope, never an inherited/default account location.
  if (Object.hasOwn(spec.env, 'HOME') && spec.env.HOME !== spec.home) throw new Error();
  const relative = path.relative(spec.home, spec.cwd);
  if (relative === '..' || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) throw new Error();
  const saved = identity(spec);
  let current = spec.home;
  for (const part of relative.split(path.sep).filter(Boolean)) {
    current = path.join(current, part);
    owned(current, 'directory', 0o700);
  }
  return saved;
}

let input, output, spec, saved, child;
let reason, parentEOF = false, closed = false, finishing = false;
let nativeCode = null, nativeSignal = null, group = null, pendingWrites = 0;
let killReady = false, settling = false, deadlineTimer, killTimer;
let pending = Buffer.alloc(0), received = false;
const closeFD = fd => { try { fs.closeSync(fd); } catch { /* no raw diagnostics */ } };
const signalName = s => ['SIGTERM', 'SIGKILL', 'SIGINT'].includes(s) ? s : s ? 'other' : null;
const groupSignal = signal => { if (group) { try { process.kill(-group, signal); } catch (e) { if (e.code === 'ESRCH') group = null; } } };
function refreshGroup() {
  if (group) { try { process.kill(-group, 0); } catch (e) { if (e.code === 'ESRCH') group = null; } }
}

function cleanup() {
  // Never remove the external lease file. Its open-file-description remains
  // locked through cleanup and belongs to Go's cross-command/recovery owner.
  identity(spec, saved);
  fs.rmSync(spec.home, { recursive: true, force: false });
}

function finish() {
  if (finishing || !closed || ((reason || settling) && !killReady) || (!reason && pendingWrites)) return;
  finishing = true;
  clearTimeout(firstTimer); clearTimeout(deadlineTimer); clearTimeout(killTimer);
  input?.destroy(); closeFD(3);
  if (reason) {
    // A dead/non-reading parent must not keep this guard alive through raw
    // stdout/stderr backpressure after the native process has been waited.
    process.stdout.destroy(); process.stderr.destroy();
  }
  if (parentEOF && spec && saved) { try { cleanup(); } catch { /* leave for locked recovery */ } }
  const resultReason = reason ?? 'exit';
  const reserved = { invalid_request: 125, spawn_failed: 126, output_limit: 122,
    deadline: 124, parent_eof: 143, cancelled: 143 };
  const exitCode = resultReason === 'exit'
    ? (Number.isInteger(nativeCode) && nativeCode >= 0 && nativeCode <= 255 ? nativeCode : null)
    : reserved[resultReason];
  const frame = Buffer.from(JSON.stringify({ protocol: 1, type: 'result', reason: resultReason,
    exitCode, signal: signalName(nativeSignal) }) + '\n');
  const code = resultReason === 'exit' ? (exitCode ?? 1) : exitCode;
  const exit = () => { closeFD(5); output?.destroy(); closeFD(4); process.exit(code); };
  // One capped safe frame only; this timer also bounds a parent that never
  // consumes result metadata. Raw data is never copied into this channel.
  setTimeout(exit, 250);
  if (frame.length > RESULT_LIMIT || !output || output.destroyed) { exit(); return; }
  try { output.end(frame, exit); } catch { exit(); }
}

function stop(cause) {
  if (finishing) return;
  if (cause === 'parent_eof') parentEOF = true;
  reason ??= cause;
  clearTimeout(firstTimer); clearTimeout(deadlineTimer);
  if (!child) { closed = true; killReady = true; finish(); return; }
  settleGroup();
}
function settleGroup() {
  settling = true;
  if (killTimer || killReady) return;
  groupSignal('SIGTERM');
  for (const stream of [child.stdout, child.stderr]) { stream?.removeAllListeners('data'); stream?.resume(); }
  killTimer = setTimeout(() => {
    killTimer = undefined;
    refreshGroup(); groupSignal('SIGKILL'); killReady = true;
    finish();
    // Keep the inherited lease until actual close; a timer must not release
    // ownership while an OS-level uninterruptible native process still lives.
  }, spec?.termGraceMs ?? 1);
}

function armDeadline() {
  const left = spec.deadlineUnixMs - Date.now();
  if (left <= 0) { stop('deadline'); return; }
  deadlineTimer = setTimeout(armDeadline, Math.min(left, 2147483647));
}
function forward(stream, destination) {
  let bytes = 0;
  stream.on('data', chunk => {
    if (reason || finishing) return;
    bytes += chunk.length;
    if (bytes > OUTPUT_LIMIT) { stop('output_limit'); return; }
    pendingWrites++;
    let flowing;
    try {
      flowing = destination.write(chunk, error => {
        pendingWrites--;
        if (error) stop('cancelled');
        finish();
      });
    } catch { pendingWrites--; stop('cancelled'); return; }
    if (!flowing) { stream.pause(); destination.once('drain', () => { if (!reason) stream.resume(); }); }
  });
  stream.on('error', () => stop('cancelled'));
}

function execute(value) {
  saved = validate(value);
  spec = value;
  if (Date.now() >= spec.deadlineUnixMs) { stop('deadline'); return; }
  armDeadline();
  const env = Object.assign(Object.create(null), spec.env, { HOME: spec.home });
  child = spawn(spec.executable, spec.args, { cwd: spec.cwd, env, detached: true, stdio: ['ignore', 'pipe', 'pipe'] });
  group = child.pid ?? null;
  child.once('error', () => stop('spawn_failed'));
  child.once('close', (code, signal) => {
    nativeCode = code; nativeSignal = signal; closed = true;
    // Once our group is gone, forget its numeric ID so delayed cleanup cannot
    // signal a reused process-group identity.
    refreshGroup();
    // Settle residual descendants before releasing ownership, while keeping
    // the native leader's actual exit status on a normal command completion.
    if (!reason && group) settleGroup();
    finish();
  });
  forward(child.stdout, process.stdout); forward(child.stderr, process.stderr);
}

process.stdout.on('error', () => stop('cancelled'));
process.stderr.on('error', () => stop('cancelled'));
process.on('SIGTERM', () => stop('cancelled'));
process.on('SIGINT', () => stop('cancelled'));
process.on('uncaughtException', () => stop('cancelled'));
process.on('unhandledRejection', () => stop('cancelled'));
const firstTimer = setTimeout(() => stop('invalid_request'), 5000);
try {
  if (process.versions.node.split('.')[0] !== '24' || process.argv.length !== 2) throw new Error();
  input = new Socket({ fd: 3, readable: true, writable: false });
  output = new Socket({ fd: 4, readable: false, writable: true });
  output.on('error', () => { /* parent may already have exited */ });
  input.on('error', () => stop('parent_eof'));
  input.on('end', () => stop(received ? 'parent_eof' : 'invalid_request'));
  input.on('data', chunk => {
    if (finishing) return;
    if (received) { stop('invalid_request'); return; }
    if (pending.length + chunk.length > FRAME_LIMIT) { stop('invalid_request'); return; }
    pending = Buffer.concat([pending, chunk]);
    const end = pending.indexOf(10);
    if (end < 0) return;
    received = true; clearTimeout(firstTimer);
    try {
      if (end !== pending.length - 1) throw new Error();
      const value = strictJSON(pending.subarray(0, end));
      pending.fill(0); pending = Buffer.alloc(0);
      execute(value);
    } catch { pending.fill(0); pending = Buffer.alloc(0); stop('invalid_request'); }
  });
} catch { stop('invalid_request'); }
