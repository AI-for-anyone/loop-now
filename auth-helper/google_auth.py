"""Fixed Google SDK authentication helper. Secrets use inherited fd3/fd4 only."""
import contextlib
import datetime
import io
import json
import math
import os
from pathlib import Path
import pwd
import signal
import stat
import sys
import time
import types
import urllib.parse

FRAME_LIMIT = 1024 * 1024
CREDENTIAL_LIMIT = 64 * 1024
MAX_EXPIRES = 253402300799999
AUTH_URI = 'https://accounts.google.com/o/oauth2/auth'
TOKEN_URI = 'https://oauth2.googleapis.com/token'
STAGES = frozenset(('provider_loaded', 'callback_ready', 'exchange_started', 'exchange_http',
                    'credential_received', 'credential_validated', 'result_queued'))


class SafeError(Exception):
    def __init__(self, code):
        self.code = code
        super().__init__('Google authentication unavailable')


def strict_json(raw):
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise SafeError('invalid_response')
            result[key] = value
        return result
    try:
        return json.loads(raw, object_pairs_hook=pairs,
                          parse_constant=lambda _: (_ for _ in ()).throw(SafeError('invalid_response')))
    except (ValueError, TypeError, UnicodeError, RecursionError):
        raise SafeError('invalid_response') from None


def text(value, limit=CREDENTIAL_LIMIT):
    if (not isinstance(value, str) or not value or value != value.strip()
            or any(ord(c) < 32 or ord(c) == 127 for c in value)):
        raise SafeError('invalid_response')
    try:
        if len(value.encode('utf-8')) > limit:
            raise SafeError('invalid_response')
    except UnicodeError:
        raise SafeError('invalid_response') from None
    return value


def expires(value):
    if type(value) is not int or not 0 < value <= MAX_EXPIRES:
        raise SafeError('invalid_response')
    return value


def expiry_millis(value):
    if not isinstance(value, datetime.datetime):
        raise SafeError('invalid_response')
    try:
        if value.tzinfo is None:
            value = value.replace(tzinfo=datetime.timezone.utc)
        millis = value.timestamp() * 1000
        if not math.isfinite(millis):
            raise SafeError('invalid_response')
        return expires(math.floor(millis))
    except (ValueError, OverflowError, OSError):
        raise SafeError('invalid_response') from None


def validate_credential(value):
    keys = {'type', 'account', 'clientId', 'clientSecret', 'refreshToken', 'accessToken',
            'expiresAt', 'scopes', 'raptToken'}
    if not isinstance(value, dict) or set(value) != keys or value['type'] != 'authorized_user':
        raise SafeError('invalid_response')
    for key in ('account', 'clientId', 'clientSecret', 'refreshToken', 'accessToken'):
        text(value[key])
    expires(value['expiresAt'])
    scopes = value['scopes']
    if not isinstance(scopes, list) or not scopes:
        raise SafeError('invalid_response')
    for scope in scopes:
        text(scope, 4096)
    if value['raptToken'] is not None:
        text(value['raptToken'])
    if len(json.dumps(value, ensure_ascii=False).encode('utf-8')) > CREDENTIAL_LIMIT:
        raise SafeError('invalid_response')
    return value


def validate_request(value):
    if (not isinstance(value, dict)
            or set(value) != {'protocol', 'operation', 'sdkRoot', 'callbackPort', 'expectedAccount', 'credential'}
            or type(value['protocol']) is not int or value['protocol'] != 1
            or value['operation'] not in ('login', 'refresh')
            or type(value['callbackPort']) is not int or value['callbackPort'] != 8085):
        raise SafeError('invalid_response')
    text(value['sdkRoot'], 4096)
    if not Path(value['sdkRoot']).is_absolute():
        raise SafeError('invalid_response')
    if value['expectedAccount'] is not None:
        text(value['expectedAccount'], 1024)
    if value['operation'] == 'login':
        if value['credential'] is not None:
            raise SafeError('invalid_response')
    else:
        validate_credential(value['credential'])
        if (value['expectedAccount'] is None or
                value['expectedAccount'].casefold() != value['credential']['account'].casefold()):
            raise SafeError('account_mismatch')
    return value


def validate_authorization_url(url, client_id):
    try:
        text(url, 16384)
        parsed = urllib.parse.urlsplit(url)
        query = urllib.parse.parse_qs(parsed.query, keep_blank_values=True, strict_parsing=True)
        required = {'client_id', 'response_type', 'redirect_uri', 'scope', 'state',
                    'code_challenge', 'code_challenge_method', 'access_type'}
        optional = {'prompt', 'include_granted_scopes', 'login_hint'}
        if (parsed.scheme != 'https' or parsed.netloc != 'accounts.google.com'
                or parsed.path != '/o/oauth2/auth' or parsed.fragment
                or not required.issubset(query) or set(query) - required - optional
                or any(len(v) != 1 or not v[0] for v in query.values())
                or query['client_id'] != [client_id] or query['response_type'] != ['code']
                or query['redirect_uri'] != ['http://localhost:8085/']
                or query['access_type'] != ['offline'] or query['code_challenge_method'] != ['S256']):
            raise SafeError('invalid_response')
        for key in ('state', 'code_challenge', 'scope'):
            text(query[key][0], 4096)
        if 'prompt' in query and (not set(query['prompt'][0].split()).issubset({'consent', 'select_account'})):
            raise SafeError('invalid_response')
        if 'include_granted_scopes' in query and query['include_granted_scopes'][0] not in ('true', 'false'):
            raise SafeError('invalid_response')
        if 'login_hint' in query:
            text(query['login_hint'][0], 1024)
        return url
    except (ValueError, UnicodeError):
        raise SafeError('invalid_response') from None


def load_sdk(root):
    root = Path(root)
    if not root.is_absolute():
        raise SafeError('helper_unavailable')
    try:
        root = root.resolve(strict=True)
        if (root / 'VERSION').read_text().strip() != '568.0.0' or sys.version_info[:3] != (3, 13, 7):
            raise SafeError('helper_unavailable')
    except OSError:
        raise SafeError('helper_unavailable') from None
    lib = root / 'lib'
    base = Path(sys.base_prefix).resolve()
    here = Path(__file__).resolve().parent
    sys.path[:] = [str(lib / 'third_party'), str(lib)] + [
        p for p in sys.path if p and 'site-packages' not in p
        and (Path(p).resolve().is_relative_to(base) or Path(p).resolve() == here)]
    from google.auth import version
    if version.__version__ != '2.50.0':
        raise SafeError('helper_unavailable')
    from googlecloudsdk.core import config
    from googlecloudsdk.core.credentials import flow, google_auth_credentials
    from googlecloudsdk.command_lib.auth.auth_util import ExtractAndValidateAccount
    from google.auth.transport.requests import Request
    from google.oauth2.credentials import Credentials
    from google.auth import exceptions
    import requests
    return types.SimpleNamespace(flow=flow, config=config, user=google_auth_credentials,
                                 extract=ExtractAndValidateAccount, Request=Request,
                                 Credentials=Credentials, exceptions=exceptions, requests=requests)


def isolated_environment():
    if any(key in os.environ for key in ('OAUTHLIB_INSECURE_TRANSPORT', 'PYTHONPATH',
                                         'PYTHONSTARTUP', 'GOOGLE_APPLICATION_CREDENTIALS')):
        raise SafeError('helper_unavailable')
    home = Path(os.environ.get('HOME', ''))
    config = Path(os.environ.get('CLOUDSDK_CONFIG', ''))
    if (not home.is_absolute() or not config.is_absolute() or not home.is_dir()
            or home.resolve() in (Path('/'), Path(pwd.getpwuid(os.getuid()).pw_dir).resolve())
            or not config.resolve().is_relative_to(home.resolve())):
        raise SafeError('helper_unavailable')
    config.mkdir(mode=0o700, parents=True, exist_ok=True)
    for path in (home, config):
        info = path.stat()
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o077:
            raise SafeError('helper_unavailable')
    os.environ['CLOUDSDK_CORE_DISABLE_FILE_LOGGING'] = 'true'
    os.environ['CLOUDSDK_CORE_DISABLE_USAGE_REPORTING'] = 'true'
    os.environ['CLOUDSDK_COMPONENT_MANAGER_DISABLE_UPDATE_CHECK'] = 'true'


class Emitter:
    def __init__(self, fd):
        self.fd = fd
        self.http_status = None

    def __call__(self, value):
        raw = json.dumps(value, ensure_ascii=False, allow_nan=False).encode('utf-8') + b'\n'
        if len(raw) > FRAME_LIMIT:
            raise SafeError('invalid_response')
        while raw:
            written = os.write(self.fd, raw)
            if written <= 0:
                raise BrokenPipeError()
            raw = raw[written:]

    def stage(self, name, status=None):
        if name not in STAGES:
            raise SafeError('helper_unavailable')
        value = {'type': 'stage', 'stage': name}
        if status is not None:
            if type(status) is not int or not 100 <= status <= 599:
                raise SafeError('invalid_response')
            value['httpStatus'] = status
            self.http_status = status
        self(value)


def credential_result(sdk, credentials, account, scopes=None, rapt=None):
    result = {'type': 'authorized_user', 'account': account,
              'clientId': credentials.client_id, 'clientSecret': credentials.client_secret,
              'refreshToken': credentials.refresh_token, 'accessToken': credentials.token,
              'expiresAt': expiry_millis(credentials.expiry),
              'scopes': list(scopes if scopes is not None else
                             (credentials.granted_scopes or credentials.scopes or [])),
              'raptToken': rapt if rapt is not None else credentials.rapt_token}
    return validate_credential(result)


def guarded_session(sdk, emit):
    session = sdk.requests.Session()
    session.trust_env = False
    original = session.send

    def send(prepared, **kwargs):
        if prepared.url != TOKEN_URI or prepared.method != 'POST':
            raise SafeError('invalid_response')
        kwargs['allow_redirects'] = False
        kwargs['timeout'] = 30
        response = original(prepared, **kwargs)
        emit.stage('exchange_http', response.status_code)
        if 300 <= response.status_code < 400:
            raise SafeError('http')
        return response

    session.send = send
    return session


def login(request, sdk, emit):
    flow = sdk.flow
    original_ports = flow._PORT_SEARCH_START, flow._PORT_SEARCH_END
    original_browser = flow.webbrowser.open
    instance = None
    flow._PORT_SEARCH_START, flow._PORT_SEARCH_END = 8085, 8086
    try:
        client = {'installed': {'client_id': sdk.config.CLOUDSDK_CLIENT_ID,
                  'client_secret': sdk.config.CLOUDSDK_CLIENT_NOTSOSECRET,
                  'auth_uri': AUTH_URI, 'token_uri': TOKEN_URI}}
        instance = flow.FullWebFlow.from_client_config(
            client, list(sdk.config.CLOUDSDK_SCOPES), autogenerate_code_verifier=True)
        emit.stage('callback_ready')

        def browser(url, **_):
            validate_authorization_url(url, sdk.config.CLOUDSDK_CLIENT_ID)
            emit({'type': 'authorization_url', 'url': url, 'callbackPort': 8085})
            return True

        flow.webbrowser.open = browser
        session = instance.oauth2session
        session.trust_env = False
        original_send = session.send
        # Retain OAuth2Session's fetch_token/state/PKCE implementation; guard its transport.
        def send(prepared, **kwargs):
            if prepared.url != TOKEN_URI or prepared.method != 'POST':
                raise SafeError('invalid_response')
            kwargs.update(allow_redirects=False, timeout=30)
            response = original_send(prepared, **kwargs)
            emit.stage('exchange_http', response.status_code)
            if 300 <= response.status_code < 400:
                raise SafeError('http')
            return response
        session.send = send
        original_fetch = instance.fetch_token
        def fetch(**kwargs):
            emit.stage('exchange_started')
            return original_fetch(**kwargs)
        instance.fetch_token = fetch
        credentials = instance.Run(prompt='consent')
        emit.stage('credential_received')
        account = sdk.extract(request['expectedAccount'], credentials)
        token_scopes = session.token.get('scope')
        scopes = token_scopes.split() if isinstance(token_scopes, str) else token_scopes
        result = credential_result(sdk, credentials, account, scopes=scopes)
        if result['expiresAt'] <= time.time() * 1000:
            raise SafeError('invalid_response')
        return result
    finally:
        if instance is not None:
            instance.server.server_close()
            instance.oauth2session.close()
        flow._PORT_SEARCH_START, flow._PORT_SEARCH_END = original_ports
        flow.webbrowser.open = original_browser


def refresh(request, sdk, emit):
    old = request['credential']
    credentials = sdk.Credentials(
        old['accessToken'], refresh_token=old['refreshToken'], token_uri=TOKEN_URI,
        client_id=old['clientId'], client_secret=old['clientSecret'], scopes=old['scopes'],
        rapt_token=old['raptToken'])
    session = guarded_session(sdk, emit)
    transport = sdk.Request(session=session)
    original = sdk.user._TokenEndpointRequestWithRetry
    # One operation, one provider attempt: never replay a possibly rotated refresh after ambiguous failure.
    sdk.user._TokenEndpointRequestWithRetry = original.__wrapped__
    try:
        emit.stage('exchange_started')
        try:
            access, rotated, expiry, response = sdk.user._RefreshGrant(
                transport, TOKEN_URI, old['refreshToken'], old['clientId'], old['clientSecret'],
                old['scopes'], old['raptToken'])
        except sdk.exceptions.RefreshError as exc:
            if any(isinstance(arg, dict) and arg.get('error') == 'invalid_grant' for arg in exc.args):
                raise SafeError('reauth_required') from None
            raise
        credentials.token, credentials._refresh_token, credentials.expiry = access, rotated, expiry
        credentials._id_token = response.get('id_token')
        emit.stage('credential_received')
        account = old['account']
        if credentials.id_token is not None:
            account = sdk.extract(request['expectedAccount'], credentials)
        scopes = old['scopes']
        if 'scope' in response:
            if not isinstance(response['scope'], str):
                raise SafeError('invalid_response')
            scopes = response['scope'].split()
            if not set(old['scopes']).issubset(scopes):
                raise SafeError('reauth_required')
        rapt = old['raptToken']
        if 'rapt_token' in response:
            rapt = text(response['rapt_token'])
        result = credential_result(sdk, credentials, account, scopes=scopes, rapt=rapt)
        if result['expiresAt'] <= time.time() * 1000:
            raise SafeError('invalid_response')
        return result
    finally:
        sdk.user._TokenEndpointRequestWithRetry = original
        session.close()


def safe_code(exc):
    if isinstance(exc, SafeError):
        return exc.code
    names = {type(exc).__name__}
    cause = exc.__cause__
    seen = set()
    while cause is not None and id(cause) not in seen and len(seen) < 10:
        seen.add(id(cause))
        names.add(type(cause).__name__)
        cause = cause.__cause__
    if 'LocalServerCreationError' in names:
        return 'callback_unavailable'
    if 'WrongAccountError' in names:
        return 'account_mismatch'
    if names & {'ReauthRequiredError', 'ReauthFailError', 'InvalidGrantError'}:
        return 'reauth_required'
    if names & {'Timeout', 'ReadTimeout', 'ConnectTimeout'}:
        return 'timeout'
    if names & {'ConnectionError', 'TransportError', 'ProxyError'}:
        return 'network'
    return 'invalid_response'


def read_request(fd):
    raw = bytearray()
    while len(raw) <= FRAME_LIMIT:
        part = os.read(fd, min(65536, FRAME_LIMIT + 1 - len(raw)))
        if not part:
            break
        raw.extend(part)
    if not raw.endswith(b'\n') or len(raw) > FRAME_LIMIT or b'\n' in raw[:-1]:
        raise SafeError('invalid_response')
    return validate_request(strict_json(raw))


class Discard(io.TextIOBase):
    def write(self, value):
        return len(value)


def main():
    emit = Emitter(4)
    original_handler = signal.getsignal(signal.SIGTERM)
    def terminate(*_):
        raise SafeError('cancelled')
    signal.signal(signal.SIGTERM, terminate)
    try:
        with contextlib.redirect_stdout(Discard()), contextlib.redirect_stderr(Discard()):
            request = read_request(3)
            isolated_environment()
            sdk = load_sdk(request['sdkRoot'])
            emit.stage('provider_loaded')
            result = (login if request['operation'] == 'login' else refresh)(request, sdk, emit)
            emit.stage('credential_validated')
            emit.stage('result_queued')
            emit({'type': 'result', 'credential': result})
        return 0
    except BrokenPipeError:
        return 1
    except BaseException as exc:
        try:
            code = safe_code(exc)
            if code == 'invalid_response' and emit.http_status is not None and emit.http_status >= 400:
                code = 'http'
            emit({'type': 'error', 'code': code})
        except (BrokenPipeError, OSError, SafeError):
            pass
        return 1
    finally:
        signal.signal(signal.SIGTERM, original_handler)


if __name__ == '__main__':
    raise SystemExit(main())
