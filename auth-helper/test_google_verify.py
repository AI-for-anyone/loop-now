"""Offline pure boundary checks; SDK/transport evidence is in the Go fixture."""
import unittest
from google_auth import SafeError
from google_verify import BoundedOutput, Guard, LIMIT


class VerificationBoundaryTests(unittest.TestCase):
    def test_unicode_multichunk_cap_counts_bytes(self):
        captured = BoundedOutput()
        captured.write('\u754c' * 20000)
        with self.assertRaises(SafeError):
            captured.write('x' * 10000)
        self.assertEqual(len(captured.getvalue().encode()), 60000)
        captured.write('x' * (LIMIT - 60000))
        with self.assertRaises(SafeError):
            captured.write('x')

    def test_fixed_endpoints_and_no_service_enable(self):
        guard = Guard()
        self.assertTrue(guard.check('GET', 'https://cloudresourcemanager.googleapis.com/v1/projects?pageSize=1'))
        self.assertFalse(guard.check('POST', 'https://oauth2.googleapis.com/token'))
        for method, url in [
            ('POST', 'https://serviceusage.googleapis.com/v1/projects/123/services/cloudresourcemanager.googleapis.com:enable'),
            ('POST', 'https://cloudresourcemanager.googleapis.com/v1/projects'),
            ('GET', 'http://cloudresourcemanager.googleapis.com/v1/projects'),
            ('GET', 'https://cloudresourcemanager.googleapis.com.evil.invalid/v1/projects'),
            ('GET', 'https://user@cloudresourcemanager.googleapis.com/v1/projects'),
            ('GET', 'https://cloudresourcemanager.googleapis.com/v1/projects#fragment'),
            ('GET', 'https://cloudresourcemanager.googleapis.com/v1/projects/123'),
            ('POST', 'https://oauth2.googleapis.com/token?target=other'),
            ('GET', 'http://metadata.google.internal/computeMetadata/v1/'),
        ]:
            with self.subTest(method=method, url=url), self.assertRaises(SafeError):
                guard.check(method, url)


if __name__ == '__main__':
    unittest.main()
