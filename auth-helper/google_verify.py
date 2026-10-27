"""Fixed CRM read verifier; no login, business project, or arbitrary CLI input."""
import contextlib
import io
import json
import os
from pathlib import Path
import sys
from urllib.parse import urlsplit

from google_auth import SafeError, isolated_environment, load_sdk
from google_runtime import Bridge, install

CRM_ORIGIN = 'https://cloudresourcemanager.googleapis.com'
TOKEN_URL = 'https://oauth2.googleapis.com/token'
LIMIT = 65536


class BoundedOutput(io.StringIO):
    def __init__(self):
        super().__init__()
        self.byte_count = 0

    def write(self, value):
        size = len(value.encode('utf-8'))
        if self.byte_count + size > LIMIT:
            raise SafeError('probe_failed')
        self.byte_count += size
        return super().write(value)


class Guard:
    def __init__(self):
        self.crm_status = None
        self.token_status = None
        self.denied = False

    def check(self, method, url):
        value = urlsplit(str(url))
        origin = value.scheme + '://' + value.netloc
        allowed = ((method.upper() == 'GET' and origin == CRM_ORIGIN
                    and value.path == '/v1/projects')
                   or (method.upper() == 'POST' and str(url) == TOKEN_URL))
        if not allowed or value.username or value.password or value.fragment:
            self.denied = True
            raise SafeError('provider_policy')
        return origin == CRM_ORIGIN

    def install(self):
        import httplib2
        import requests
        original_http = httplib2.Http.request
        original_request = requests.sessions.Session.request
        guard = self

        def http(client, uri, method='GET', *args, **kwargs):
            crm = guard.check(method, uri)
            # Disable redirects at the real native transport, not only the
            # outer SDK wrapper. A redirect is never retried at another origin.
            client.follow_redirects = False
            kwargs['redirections'] = 0
            response, content = original_http(client, uri, method, *args, **kwargs)
            if crm:
                guard.crm_status = int(response.status)
            else:
                guard.token_status = int(response.status)
            return response, content

        def request(client, method, url, **kwargs):
            crm = guard.check(method, url)
            client.trust_env = False
            kwargs['allow_redirects'] = False
            kwargs['timeout'] = min(float(kwargs.get('timeout') or 10), 10)
            response = original_request(client, method, url, **kwargs)
            if crm:
                guard.crm_status = int(response.status_code)
            else:
                guard.token_status = int(response.status_code)
            return response

        # Pin this one CRM client's read to one attempt. SDK568's generated
        # BaseApiClient otherwise retries 429 five times with backoff. This is
        # private to the single verifier process and does not alter runtime CLI.
        from googlecloudsdk.generated_clients.apis.cloudresourcemanager.v1 import cloudresourcemanager_v1_client
        client_type = cloudresourcemanager_v1_client.CloudresourcemanagerV1
        original_init = client_type.__init__

        def init(client, *args, **kwargs):
            original_init(client, *args, **kwargs)
            client.num_retries = 0

        client_type.__init__ = init
        httplib2.Http.request = http
        requests.sessions.Session.request = request


def run(sdk_root):
    isolated_environment()
    for key, value in {
        'CLOUDSDK_CORE_SHOULD_PROMPT_TO_ENABLE_API': 'false',
        'CLOUDSDK_CORE_DISABLE_PROMPTS': 'true',
        'CLOUDSDK_CORE_DISABLE_FILE_LOGGING': 'true',
        'CLOUDSDK_METRICS_ENVIRONMENT': 'none',
    }.items():
        os.environ[key] = value
    guard = Guard()
    captured, diagnostic = BoundedOutput(), BoundedOutput()
    status = 1
    try:
        # SDK logging caches stream handles while importing. Redirect before
        # load_sdk, not merely around gcloud.main, so native output can never
        # bypass the private bounded capture and contaminate the safe channel.
        with contextlib.redirect_stdout(captured), contextlib.redirect_stderr(diagnostic):
            sdk = load_sdk(sdk_root)
            selector = os.environ.get('LOOP_NOW_GCLOUD_GRANT_VERSION')
            if selector:
                if os.environ.get('CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE') != selector:
                    raise SafeError('configuration_invalid')
                install(sdk, selector, Bridge(os.environ.get('LOOP_NOW_GCLOUD_BRIDGE_SOCKET', ''),
                                             os.environ.get('LOOP_NOW_GCLOUD_BRIDGE_CAPABILITY', '')))
            else:
                credential = Path(os.environ['HOME']) / '.loop-now/gcloud/service-account.json'
                if (not credential.is_file() or credential.is_symlink()
                        or credential.stat().st_mode & 0o077
                        or os.environ.get('CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE') != str(credential)):
                    raise SafeError('configuration_invalid')
            guard.install()
            import gcloud
            sys.argv = ['gcloud', 'projects', 'list', '--limit=1', '--format=json', '--quiet']
            try:
                status = gcloud.main()
            except SystemExit as exc:
                status = exc.code
    except BaseException:
        pass
    passed = False
    if not guard.denied and status in (0, None) and guard.crm_status == 200:
        try:
            passed = isinstance(json.loads(captured.getvalue()), list)
        except (ValueError, TypeError):
            pass
    http_status = guard.crm_status if guard.crm_status is not None else guard.token_status
    code = None if passed else 'probe_failed'
    if not passed:
        if guard.denied or http_status == 403:
            code = 'provider_policy'
        elif http_status == 401:
            code = 'authentication_failed'
        elif http_status == 429:
            code = 'rate_limited'
    # No SDK stdout/stderr or HTTP response content reaches this channel.
    return {'passed': passed, 'errorCode': code, 'httpStatus': http_status}


def main():
    result = {'passed': False, 'errorCode': 'configuration_invalid', 'httpStatus': None}
    try:
        if len(sys.argv) != 3 or sys.argv[1] != '--sdk-root':
            raise SafeError('configuration_invalid')
        result = run(sys.argv[2])
    except BaseException:
        pass
    sys.stdout.write(json.dumps(result, separators=(',', ':'), allow_nan=False) + '\n')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
