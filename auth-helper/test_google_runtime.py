"""Private Unix protocol adversarial cases and actual gcloud multi-process renewal."""
import concurrent.futures
import http.server
import json
import os
from pathlib import Path
import socket
import tempfile
import threading
import time
import unittest
import uuid
from unittest.mock import patch

import google_auth as auth
import google_runtime as runtime
from test_google_auth import PYTHON, SDK, launch_python


class UnixFixture:
    def __init__(self, path, responder):
        self.path = path
        self.responder = responder
        self.requests = []
        self.stop = threading.Event()
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.bind(str(path))
        path.chmod(0o600)
        self.sock.listen()
        self.sock.settimeout(0.1)
        self.workers = []
        self.thread = threading.Thread(target=self.serve, daemon=True)
        self.thread.start()

    def serve(self):
        while not self.stop.is_set():
            try:
                conn, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                break
            worker = threading.Thread(target=self.handle, args=(conn,), daemon=True)
            self.workers.append(worker)
            worker.start()

    def handle(self, conn):
        with conn:
            conn.settimeout(2)
            with conn.makefile('rb') as stream:
                raw = stream.readline(auth.FRAME_LIMIT + 1)
                request = json.loads(raw)
                self.requests.append(request)
                response = self.responder(request)
                try:
                    conn.sendall(response)
                except (BrokenPipeError, ConnectionResetError):
                    pass

    def close(self):
        self.stop.set()
        self.sock.close()
        self.thread.join(timeout=2)
        for worker in self.workers:
            worker.join(timeout=2)
        assert not self.thread.is_alive() and not any(t.is_alive() for t in self.workers)


class BridgeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='gcb-', dir='/tmp')
        self.root = Path(self.temp.name).resolve()

    def tearDown(self):
        self.temp.cleanup()

    def test_frames_ids_expiry_and_forbidden(self):
        modes = ('success', 'wrong_id', 'unknown', 'two_frames', 'truncated', 'oversized',
                 'bad_expiry', 'expired', 'oversized_access', 'forbidden', 'requires_login', 'duplicate')
        for mode in modes:
            with self.subTest(mode=mode):
                path = self.root / (mode + '.sock')
                def responder(request):
                    self.assertEqual(set(request), {'protocol', 'type', 'requestId', 'capability'})
                    self.assertEqual(request['protocol'], 1)
                    self.assertEqual(request['type'], 'resolve')
                    uuid.UUID(request['requestId'])
                    reply = {'type': 'credential', 'requestId': request['requestId'],
                             'accessToken': 'synthetic-access', 'expiresAt': int(time.time() * 1000) + 600000}
                    if mode == 'wrong_id':
                        reply['requestId'] = str(uuid.uuid4())
                    elif mode == 'unknown':
                        reply['grantVersion'] = str(uuid.uuid4())
                    elif mode == 'bad_expiry':
                        reply['expiresAt'] = True
                    elif mode == 'expired':
                        reply['expiresAt'] = 1
                    elif mode == 'oversized_access':
                        reply['accessToken'] = 'x' * 65537
                    elif mode in ('forbidden', 'requires_login'):
                        reply = {'type': 'error', 'requestId': request['requestId'], 'code': mode}
                    raw = json.dumps(reply).encode() + b'\n'
                    if mode == 'two_frames':
                        raw *= 2
                    elif mode == 'truncated':
                        raw = raw[:-2]
                    elif mode == 'oversized':
                        raw = b'x' * (auth.FRAME_LIMIT + 1)
                    elif mode == 'duplicate':
                        raw = raw[:-2] + b',"type":"credential"}\n'
                    return raw
                fixture = UnixFixture(path, responder)
                try:
                    bridge = runtime.Bridge(path, 'synthetic-capability')
                    if mode == 'success':
                        self.assertEqual(bridge.resolve()[0], 'synthetic-access')
                    else:
                        with self.assertRaises((auth.SafeError, OSError)):
                            bridge.resolve()
                finally:
                    fixture.close()

    def test_disconnect_deadline_and_permissions(self):
        path = self.root / 'deadline.sock'
        fixture = UnixFixture(path, lambda _: (time.sleep(0.15), b'')[1])
        try:
            bridge = runtime.Bridge(path, 'synthetic-capability')
            with patch.object(runtime, 'TIMEOUT', 0.03), self.assertRaises((auth.SafeError, TimeoutError)):
                bridge.resolve()
            with self.assertRaises(auth.SafeError):
                bridge.resolve()
            path.chmod(0o666)
            with self.assertRaises(auth.SafeError):
                runtime.Bridge(path, 'synthetic-capability')
        finally:
            fixture.close()


class ActualRuntimeTests(unittest.TestCase):
    def test_two_processes_three_pages_and_clones(self):
        if not SDK.exists() or not PYTHON.exists():
            self.skipTest('Fixed private SDK/Python installation proof not available')
        with tempfile.TemporaryDirectory(prefix='gcr-', dir='/tmp') as temp:
            root = Path(temp).resolve()
            version = str(uuid.uuid4())
            epoch = int(time.time()) + 60
            clock = root / 'clock'
            clock.write_text(str(epoch))
            state = {'generation': -1, 'refreshes': [], 'api': []}
            lock = threading.Lock()
            barrier = threading.Barrier(2, timeout=10)

            def resolve(request):
                self.assertIn(request['capability'], ('synthetic-capability-0', 'synthetic-capability-1'))
                self.assertEqual(set(request), {'protocol', 'type', 'requestId', 'capability'})
                with lock:
                    generation = (int(clock.read_text()) - epoch) // 600
                    if generation != state['generation']:
                        state['generation'] = generation
                        state['refreshes'].append(generation)
                    reply = {'type': 'credential', 'requestId': request['requestId'],
                             'accessToken': 'synthetic-generation-' + str(generation),
                             'expiresAt': (epoch + generation * 600 + 500) * 1000}
                return json.dumps(reply).encode() + b'\n'

            unix = UnixFixture(root / 'bridge.sock', resolve)
            class API(http.server.BaseHTTPRequestHandler):
                def log_message(self, *_):
                    pass
                def do_GET(self):
                    assert self.path.startswith('/v1/projects')
                    page = 2 if 'pageToken=second' in self.path else 3 if 'pageToken=third' in self.path else 1
                    with lock:
                        state['api'].append((page, self.headers.get('Authorization')))
                    if page < 3:
                        number = barrier.wait()
                        if number == 0:
                            clock.write_text(str(epoch + page * 600))
                        barrier.wait()
                    body = {'projects': [{'projectId': 'fixture-' + str(page), 'name': 'fixture', 'lifecycleState': 'ACTIVE'}]}
                    if page < 3:
                        body['nextPageToken'] = 'second' if page == 1 else 'third'
                    raw = json.dumps(body).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
            api = http.server.ThreadingHTTPServer(('127.0.0.1', 0), API)
            thread = threading.Thread(target=api.serve_forever, daemon=True)
            thread.start()
            processes = []
            try:
                denied_root = root / 'selector-denied'
                denied_root.mkdir(mode=0o700)
                denied = launch_python(denied_root, [str(Path(__file__).with_name('google_runtime.py'))],
                    ['--sdk-root', SDK, '--', 'projects', 'list'],
                    extra={'LOOP_NOW_GCLOUD_GRANT_VERSION': version,
                           'CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE': str(uuid.uuid4()),
                           'LOOP_NOW_GCLOUD_BRIDGE_SOCKET': str(unix.path),
                           'LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY': 'synthetic-capability-0'},
                    token_port=api.server_address[1], unix_socket=unix.path)
                processes.append(denied)
                out, err = denied.communicate(timeout=3)
                self.assertEqual(denied.returncode, 1)
                self.assertEqual(out, b'')
                self.assertEqual(err, b'Google authorization unavailable; login may be required.\n')
                self.assertEqual(unix.requests, [])
                self.assertEqual(state['api'], [])
                # All fixture changes live in this test launcher, not production bootstrap/config.
                code = '''import sys,datetime
sys.path.insert(0,sys.argv[1])
import google_runtime as r
clock_path=sys.argv[2]
original_load=r.load_sdk
def load(path):
 sdk=original_load(path)
 from google.auth import _helpers
 from pathlib import Path
 _helpers.utcnow=lambda:datetime.datetime.fromtimestamp(int(Path(clock_path).read_text()),datetime.timezone.utc).replace(tzinfo=None)
 return sdk
r.load_sdk=load
original_factory=r.broker_credentials
def factory(sdk,bridge,scopes):
 value=original_factory(sdk,bridge,scopes)
 expected_expiry=datetime.datetime(2030,1,1)
 value.token='synthetic-copied-access'
 value.expiry=expected_expiry
 for clone in [lambda v:v.with_quota_project('fixture-quota'),lambda v:v.with_account('fixture@example.invalid'),lambda v:v.with_universe_domain('googleapis.com')]:
  value=clone(value)
  assert value.refresh_handler is not None and value.refresh_token is None and value.client_secret is None
  assert value.expiry==expected_expiry and value.token=='synthetic-copied-access'
 value.token=None
 value.expiry=None
 return value
r.broker_credentials=factory
original_install=r.install
def install(sdk,selector,bridge):
 original_install(sdk,selector,bridge)
 from googlecloudsdk.core.credentials import store
 for _ in range(2):
  value=store._LoadFromFileOverride(selector,list(sdk.config.CLOUDSDK_SCOPES))
  assert value.refresh_handler is not None and value.refresh_token is None and value.client_secret is None
 try:
  store._LoadFromFileOverride('not-the-captured-selector',[])
 except sdk.exceptions.RefreshError:
  pass
 else:
  raise AssertionError('Unexpected selector accepted')
r.install=install
raise SystemExit(r.main(['--sdk-root',sys.argv[3],'--','projects','list','--limit=3','--page-size=1','--format=json','--quiet']))
'''
                for i in range(2):
                    work = root / ('process-' + str(i))
                    work.mkdir(mode=0o700)
                    extra = {'LOOP_NOW_GCLOUD_GRANT_VERSION': version,
                             'CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE': version,
                             'LOOP_NOW_GCLOUD_BRIDGE_SOCKET': str(unix.path),
                             'LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY': 'synthetic-capability-' + str(i),
                             'CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDRESOURCEMANAGER': 'http://127.0.0.1:' + str(api.server_address[1]) + '/'}
                    processes.append(launch_python(work, ['-c', code],
                        [Path(__file__).parent, clock, SDK], extra=extra,
                        token_port=api.server_address[1], unix_socket=unix.path))
                for proc in processes[1:]:
                    out, err = proc.communicate(timeout=20)
                    self.assertEqual(proc.returncode, 0, err.decode())
                    self.assertEqual(err, b'')
                    self.assertEqual(len(json.loads(out)), 3)
                self.assertEqual(state['refreshes'], [0, 1, 2])
                self.assertEqual(len(state['api']), 6)
                self.assertTrue(all(token == 'Bearer synthetic-generation-' + str(page - 1)
                                    for page, token in state['api']))
            finally:
                for proc in processes:
                    if proc.poll() is None:
                        proc.kill()
                        proc.wait()
                api.shutdown()
                api.server_close()
                thread.join(timeout=2)
                unix.close()
                self.assertFalse(thread.is_alive())


if __name__ == '__main__':
    unittest.main()
