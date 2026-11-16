package clitemplate

import "os/exec"

// ResolveGooglePython returns the path of the Python interpreter that runs the Google SDK.
// It prefers python3.13 (the verification helpers for gcloud 568 were reviewed and pinned against 3.13.7), falls
// back to python3 when that is missing (only for checks such as --version that do not load the SDK dependencies),
// and returns an empty string when neither exists.
func ResolveGooglePython() string {
	for _, name := range []string{"python3.13", "python3"} {
		if p, e := exec.LookPath(name); e == nil {
			return p
		}
	}
	return ""
}
