"""Pure validation plus real fixed SDK/helper, synthetic loopback only."""
import base64
import datetime
import hashlib
import http.server
import json
import os
from pathlib import Path
import select
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.parse
import urllib.request

import google_auth as auth

REPO = Path(__file__).resolve().parents[1]
PROOF = REPO / '.local-build/cli-installation-proof'
SDK = PROOF / 'unpacked/google-cloud-sdk'
FW = PROOF / 'unpacked/private-python/Python.framework'
PYTHON = FW / 'Versions/3.13/bin/python3.13'


def credential():
    return {'type': 'authorized_user', 'account': 'fixture@example.invalid',
            'clientId': 'synthetic-client', 'clientSecret': 'synthetic-client-secret',
            'refreshToken': 'synthetic-refresh', 'accessToken': 'synthetic-access',
            'expiresAt': int(time.time() * 1000) + 3600000,
            'scopes': ['https://www.googleapis.com/auth/cloud-platform'], 'raptToken': 'synthetic-old-rapt'}


def sandbox(root, token_port=None, unix_socket=None):
    profile = root / 'offline.sb'
    body = ('(version 1)(allow default)(deny network*)'
            '(deny file-write* (require-not (subpath ' + json.dumps(str(root)) + ')))'
            '(deny file-read* (subpath "/opt/homebrew")'
            '(subpath "/Library/Frameworks/Python.framework")'
            '(subpath "/Users/ljl/.config") (subpath "/Users/ljl/Library/Keychains"))')
    if token_port:
        body += '(allow network-outbound (remote tcp "localhost:' + str(token_port) + '"))'
    body += '(allow network-bind (local tcp "localhost:8085"))'
    body += '(allow network-inbound (local tcp "localhost:8085"))'
    if unix_socket:
        body += '(allow network-outbound (remote unix-socket (path ' + json.dumps(str(unix_socket)) + ')))'
    profile.write_text(body)
    return profile


def launch_python(root, script, args=(), pass_fds=(), extra=None, token_port=None, unix_socket=None):
    home = root / 'home'
    home.mkdir(mode=0o700, exist_ok=True)
    tmp = root / 'tmp'
    tmp.mkdir(exist_ok=True)
    env = {'HOME': str(home), 'CLOUDSDK_CONFIG': str(home / 'gcloud'),
           'PATH': '/usr/bin:/bin', 'TMPDIR': str(tmp), 'LANG': 'C',
           'PYTHONHOME': str(FW / 'Versions/3.13'), 'PYTHONNOUSERSITE': '1'}
    env.update(extra or {})
    command = ['/usr/bin/sandbox-exec', '-f', str(sandbox(root, token_port, unix_socket)),
               '/bin/sh', '-c',
               'export DYLD_FRAMEWORK_PATH="$1" DYLD_LIBRARY_PATH="$2"; shift 2; exec "$@"',
               'private-python', str(FW.parent), str(FW / 'Versions/3.13/lib'), str(PYTHON), '-S',
               *script, *map(str, args)]
    return subprocess.Popen(command, cwd=root, env=env, pass_fds=pass_fds,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)


class ValidationTests(unittest.TestCase):
    def test_authorization_url_negative_matrix(self):
        query = {'client_id': 'fixture-client', 'response_type': 'code',
                 'redirect_uri': 'http://localhost:8085/', 'scope': 'openid', 'state': 'synthetic-state',
                 'code_challenge': 'synthetic-challenge', 'code_challenge_method': 'S256',
                 'access_type': 'offline', 'prompt': 'consent'}
        url = auth.AUTH_URI + '?' + urllib.parse.urlencode(query)
        self.assertEqual(auth.validate_authorization_url(url, 'fixture-client'), url)
        bad_urls = [url.replace('accounts.google.com', 'example.invalid'),
                    url.replace('/o/oauth2/auth?', '/invalid?'), url + '#fragment',
                    url + '&state=second', url + '&refresh_token=secret', url + '&unknown=x']
        for key, value in [('redirect_uri', 'http://localhost:1455/'), ('response_type', 'token'),
                           ('code_challenge_method', 'plain'), ('include_granted_scopes', '1')]:
            bad_urls.append(auth.AUTH_URI + '?' + urllib.parse.urlencode(dict(query, **{key: value})))
        for bad in bad_urls:
            with self.subTest(kind='unsafe URL'), self.assertRaises(auth.SafeError):
                auth.validate_authorization_url(bad, 'fixture-client')

    def test_strict_json_and_contract(self):
        for raw in (b'{"x":1,"x":2}', b'{"x":NaN}', b'[]x', b'"\xff"'):
            with self.subTest(raw=raw), self.assertRaises(auth.SafeError):
                auth.strict_json(raw)
        value = credential()
        self.assertIs(auth.validate_credential(value), value)
        for key, replacement in [('expiresAt', True), ('expiresAt', 1.25), ('expiresAt', 0),
                                 ('accessToken', ''), ('raptToken', 4), ('scopes', []),
                                 ('refreshToken', 'x' * 65536)]:
            bad = dict(value, **{key: replacement})
            with self.subTest(key=key), self.assertRaises(auth.SafeError):
                auth.validate_credential(bad)
        request = {'protocol': 1, 'operation': 'refresh', 'sdkRoot': str(SDK),
                   'callbackPort': 8085, 'expectedAccount': value['account'], 'credential': value}
        auth.validate_request(request)
        with self.assertRaises(auth.SafeError) as err:
            auth.validate_request(dict(request, expectedAccount='other@example.invalid'))
        self.assertEqual(err.exception.code, 'account_mismatch')
        for change in ({'extra': 1}, {'protocol': True}, {'callbackPort': 1455}):
            with self.assertRaises(auth.SafeError):
                auth.validate_request(dict(request, **change))

    def test_expiry_floor_and_safe_exception(self):
        expiry = datetime.datetime(2026, 1, 1, 0, 0, 0, 123456)
        self.assertEqual(auth.expiry_millis(expiry), 1767225600123)
        self.assertEqual(str(auth.SafeError('http')), 'Google authentication unavailable')


def b64(data):
    return base64.urlsafe_b64encode(data).rstrip(b'=').decode()


class ActualHelperTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not PYTHON.exists() or not SDK.exists():
            raise unittest.SkipTest('Fixed private SDK/Python installation proof not available')

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='google-helper-', dir=PROOF)
        self.root = Path(self.temp.name)
        self.mode = 'success'
        self.posts = 0
        self.query = None
        self.pkce = False
        self.refresh_body = None
        self.redirect_gets = 0
        self.workers = []
        owner = self

        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                owner.redirect_gets += 1
                self.send_error(500)

            def do_POST(self):
                assert self.path == '/token'
                owner.posts += 1
                body = urllib.parse.parse_qs(self.rfile.read(int(self.headers['Content-Length'])).decode())
                owner.refresh_body = body
                scopes = body.get('scope', ['https://www.googleapis.com/auth/cloud-platform'])[0]
                if body['grant_type'] == ['authorization_code']:
                    verifier = body['code_verifier'][0]
                    owner.pkce = b64(hashlib.sha256(verifier.encode()).digest()) == owner.query['code_challenge'][0]
                    scopes = owner.query['scope'][0]
                token = '.'.join([b64(b'{"alg":"none"}'),
                                  b64(b'{"email":"fixture@example.invalid"}'), ''])
                reply = {'access_token': 'synthetic-new-access', 'refresh_token': 'synthetic-new-refresh',
                         'expires_in': 3600.0001, 'token_type': 'Bearer', 'scope': scopes, 'id_token': token}
                status = 200
                if owner.mode == 'missing_refresh':
                    reply.pop('refresh_token')
                if owner.mode == 'no_id':
                    reply.pop('id_token')
                if owner.mode in ('bad_rapt', 'rotate'):
                    reply['rapt_token'] = 17 if owner.mode == 'bad_rapt' else 'synthetic-new-rapt'
                if owner.mode in ('invalid_grant', 'http_500'):
                    status = 400 if owner.mode == 'invalid_grant' else 500
                    reply = {'error': 'invalid_grant' if status == 400 else 'server_error',
                             'error_description': 'synthetic-provider-secret-marker'}
                if owner.mode == 'redirect':
                    status = 302
                raw = b'{broken' if owner.mode == 'bad_json' else json.dumps(reply).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                if owner.mode == 'redirect':
                    self.send_header('Location', 'http://127.0.0.1:' + str(owner.server.server_address[1]) + '/second')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        for proc in self.workers:
            if proc.poll() is None:
                proc.kill()
                proc.wait()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.assertFalse(self.thread.is_alive())
        self.temp.cleanup()

    def start(self, operation='login', expected=None, value=None, fixture=True):
        read_in, write_in = os.pipe()
        read_out, write_out = os.pipe()
        code = ('import os,sys,fcntl; sys.path.insert(0,sys.argv[1]); import google_auth as g; '
                'r=fcntl.fcntl(int(sys.argv[2]),fcntl.F_DUPFD,10); w=fcntl.fcntl(int(sys.argv[3]),fcntl.F_DUPFD,10); '
                'os.dup2(r,3); os.dup2(w,4); os.close(r); os.close(w); ')
        if fixture:
            code += ('g.TOKEN_URI="http://127.0.0.1:' + str(self.server.server_address[1]) + '/token"; '
                     'original=g.isolated_environment; '
                     'g.isolated_environment=lambda:(original(),os.environ.update(OAUTHLIB_INSECURE_TRANSPORT="1")); ')
        code += 'raise SystemExit(g.main())'
        proc = launch_python(self.root, ['-c', code],
                             [Path(__file__).parent, read_in, write_out],
                             (read_in, write_out), token_port=self.server.server_address[1])
        self.workers.append(proc)
        os.close(read_in)
        os.close(write_out)
        request = {'protocol': 1, 'operation': operation, 'sdkRoot': str(SDK), 'callbackPort': 8085,
                   'expectedAccount': expected, 'credential': value}
        os.write(write_in, json.dumps(request).encode() + b'\n')
        os.close(write_in)
        return proc, os.fdopen(read_out, 'rb', buffering=0)

    def read(self, stream):
        self.assertTrue(select.select([stream], [], [], 5)[0], 'Helper IPC deadline')
        raw = stream.readline(auth.FRAME_LIMIT + 1)
        self.assertLessEqual(len(raw), auth.FRAME_LIMIT)
        self.assertTrue(raw.endswith(b'\n'))
        return json.loads(raw)

    def finish(self, proc, stream, callback='valid', cancel=False):
        events = []
        try:
            while True:
                event = self.read(stream)
                events.append(event)
                if event['type'] == 'authorization_url':
                    parsed = urllib.parse.urlparse(event['url'])
                    self.assertEqual(parsed.netloc, 'accounts.google.com')
                    self.query = urllib.parse.parse_qs(parsed.query)
                    self.assertEqual(self.query['prompt'], ['consent'])
                    self.assertEqual(self.query['redirect_uri'], ['http://localhost:8085/'])
                    if cancel:
                        proc.terminate()
                        continue
                    values = {'state': self.query['state'][0], 'code': 'synthetic-code'}
                    if callback == 'wrong_state':
                        values['state'] = 'wrong-state'
                    with urllib.request.urlopen('http://127.0.0.1:8085/?' + urllib.parse.urlencode(values), timeout=3) as response:
                        self.assertEqual(response.status, 200)
                        response.read(100000)
                if event['type'] in ('result', 'error'):
                    break
            out, err = proc.communicate(timeout=5)
            self.assertEqual(out, b'')
            self.assertEqual(err, b'')
            if callback or cancel:
                with self.assertRaises(ConnectionRefusedError):
                    socket.create_connection(('127.0.0.1', 8085), timeout=0.2)
            return events
        finally:
            stream.close()

    def test_actual_login_success_and_state_rejection(self):
        for callback in ('valid', 'wrong_state'):
            with self.subTest(callback=callback):
                self.posts = 0
                proc, stream = self.start()
                events = self.finish(proc, stream, callback)
                if callback == 'valid':
                    result = events[-1]['credential']
                    auth.validate_credential(result)
                    self.assertTrue(self.pkce)
                    self.assertEqual(result['account'], 'fixture@example.invalid')
                    self.assertEqual(type(result['expiresAt']), int)
                    self.assertIn('exchange_http', [e.get('stage') for e in events])
                    self.assertEqual(events[-2], {'type': 'stage', 'stage': 'result_queued'})
                else:
                    self.assertEqual(events[-1], {'type': 'error', 'code': 'invalid_response'})
                    self.assertEqual(self.posts, 0)

    def test_missing_refresh_wrong_account_and_cancel(self):
        self.mode = 'missing_refresh'
        proc, stream = self.start()
        self.assertEqual(self.finish(proc, stream)[-1], {'type': 'error', 'code': 'invalid_response'})
        self.mode = 'success'
        proc, stream = self.start(expected='other@example.invalid')
        self.assertEqual(self.finish(proc, stream)[-1], {'type': 'error', 'code': 'account_mismatch'})
        self.posts = 0
        proc, stream = self.start()
        self.assertEqual(self.finish(proc, stream, cancel=True)[-1], {'type': 'error', 'code': 'cancelled'})
        self.assertEqual(self.posts, 0)

    def test_occupied_port_fails_without_fallback(self):
        with socket.socket() as held:
            held.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            held.bind(('127.0.0.1', 8085))
            held.listen()
            proc, stream = self.start()
            events = []
            try:
                events.append(self.read(stream))
                events.append(self.read(stream))
                self.assertEqual(events[-1], {'type': 'error', 'code': 'callback_unavailable'})
                self.assertFalse(any(e['type'] == 'authorization_url' for e in events))
                out, err = proc.communicate(timeout=3)
                self.assertEqual((out, err), (b'', b''))
            finally:
                stream.close()

    def test_actual_refresh_rotation_and_no_replay(self):
        for mode, error in [('rotate', None), ('missing_refresh', None), ('no_id', None),
                            ('bad_rapt', 'invalid_response'), ('bad_json', 'invalid_response'),
                            ('invalid_grant', 'reauth_required'), ('http_500', 'http'), ('redirect', 'http')]:
            with self.subTest(mode=mode):
                self.mode, self.posts = mode, 0
                old = credential()
                proc, stream = self.start('refresh', old['account'], old)
                events = self.finish(proc, stream, callback=None)
                self.assertEqual(self.posts, 1)
                self.assertEqual(self.redirect_gets, 0)
                self.assertEqual(self.refresh_body['rapt'], ['synthetic-old-rapt'])
                if error:
                    self.assertEqual(events[-1], {'type': 'error', 'code': error})
                else:
                    result = events[-1]['credential']
                    self.assertEqual(result['refreshToken'], old['refreshToken'] if mode == 'missing_refresh' else 'synthetic-new-refresh')
                    self.assertEqual(result['raptToken'], 'synthetic-new-rapt' if mode == 'rotate' else old['raptToken'])
                    self.assertEqual(result['account'], old['account'])


if __name__ == '__main__':
    unittest.main()
