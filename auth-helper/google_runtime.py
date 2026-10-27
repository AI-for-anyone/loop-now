"""Fixed SDK gcloud entry point with a version-bound private access-token broker."""
import datetime
import json
import os
from pathlib import Path
import socket
import stat
import sys
import time
import uuid

from google_auth import (FRAME_LIMIT, CREDENTIAL_LIMIT, SafeError, expires,
                         isolated_environment, load_sdk, strict_json, text)

TIMEOUT = 30


class Bridge:
    def __init__(self, path, capability):
        self.path = str(path)
        self.capability = text(capability, 4096)
        if not Path(path).is_absolute():
            raise SafeError('helper_unavailable')
        parent = Path(path).parent.stat()
        info = Path(path).lstat()
        if (not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid()
                or stat.S_IMODE(info.st_mode) != 0o600 or parent.st_uid != os.getuid()
                or stat.S_IMODE(parent.st_mode) != 0o700):
            raise SafeError('helper_unavailable')

    def resolve(self, request=None, scopes=None):
        del request, scopes
        request_id = str(uuid.uuid4())
        frame = json.dumps({'protocol': 1, 'type': 'resolve', 'requestId': request_id,
                            'capability': self.capability}).encode() + b'\n'
        if len(frame) > FRAME_LIMIT:
            raise SafeError('helper_unavailable')
        deadline = time.monotonic() + TIMEOUT
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
            def remaining():
                left = deadline - time.monotonic()
                if left <= 0:
                    raise SafeError('timeout')
                sock.settimeout(left)
            remaining()
            sock.connect(self.path)
            remaining()
            sock.sendall(frame)
            raw = bytearray()
            while True:
                remaining()
                part = sock.recv(min(65536, FRAME_LIMIT + 1 - len(raw)))
                if not part:
                    break
                raw.extend(part)
                if len(raw) > FRAME_LIMIT:
                    raise SafeError('helper_unavailable')
            if not raw.endswith(b'\n') or b'\n' in raw[:-1]:
                raise SafeError('helper_unavailable')
        reply = strict_json(raw)
        if not isinstance(reply, dict) or reply.get('requestId') != request_id:
            raise SafeError('helper_unavailable')
        if reply.get('type') == 'error':
            if set(reply) != {'type', 'requestId', 'code'} or reply['code'] not in {
                    'unavailable', 'requires_login', 'forbidden', 'timeout'}:
                raise SafeError('helper_unavailable')
            raise SafeError(reply['code'])
        if set(reply) != {'type', 'requestId', 'accessToken', 'expiresAt'} or reply['type'] != 'credential':
            raise SafeError('helper_unavailable')
        access = text(reply['accessToken'], CREDENTIAL_LIMIT)
        expiry = expires(reply['expiresAt'])
        if expiry <= time.time() * 1000:
            raise SafeError('helper_unavailable')
        return access, datetime.datetime(1970, 1, 1) + datetime.timedelta(milliseconds=expiry)


def broker_credentials(sdk, bridge, scopes):
    class BrokerCredentials(sdk.Credentials):
        def _make_copy(self):
            result = super()._make_copy()
            result.refresh_handler = self.refresh_handler
            result.expiry = self.expiry
            return result

    def refresh(request, scopes=None):
        try:
            return bridge.resolve(request, scopes)
        except BaseException:
            # Neither capability nor provider/IPC body may reach the native SDK diagnostic channel.
            raise sdk.exceptions.RefreshError('Google authorization unavailable; login may be required.') from None

    return BrokerCredentials(None, scopes=scopes, refresh_handler=refresh)


def install(sdk, selector, bridge):
    from googlecloudsdk.core.credentials import store
    def load(path, scopes):
        if path != selector:
            raise sdk.exceptions.RefreshError('Google authorization selector rejected.')
        return broker_credentials(sdk, bridge, scopes)
    store._LoadFromFileOverride = load


def main(argv=None):
    args = list(sys.argv[1:] if argv is None else argv)
    try:
        if len(args) < 4 or args[0] != '--sdk-root' or args[2] != '--':
            raise SafeError('helper_unavailable')
        if any(key in os.environ for key in (
                'GOOGLE_APPLICATION_CREDENTIALS', 'CLOUDSDK_AUTH_ACCESS_TOKEN',
                'CLOUDSDK_AUTH_ACCESS_TOKEN_FILE', 'OAUTHLIB_INSECURE_TRANSPORT',
                'PYTHONPATH', 'PYTHONSTARTUP')):
            raise SafeError('helper_unavailable')
        selector = os.environ.get('LOOP_NOW_GCLOUD_GRANT_VERSION', '')
        if str(uuid.UUID(selector)) != selector or uuid.UUID(selector).int == 0:
            raise SafeError('helper_unavailable')
        if os.environ.get('CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE') != selector:
            raise SafeError('helper_unavailable')
        isolated_environment()
        bridge = Bridge(os.environ.get('LOOP_NOW_GCLOUD_BRIDGE_SOCKET', ''),
                        os.environ.get('LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY', ''))
        sdk = load_sdk(args[1])
        install(sdk, selector, bridge)
        import gcloud
        sys.argv = ['gcloud', *args[3:]]
        return gcloud.main()
    except SystemExit as exc:
        return exc.code
    except BaseException:
        sys.stderr.write('Google authorization unavailable; login may be required.\n')
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
