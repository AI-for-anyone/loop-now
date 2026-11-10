package folders

import (
	"strings"
	"testing"
)

func TestClassifyCloneFailure(t *testing.T) {
	cases := []struct {
		url, out, code string
	}{
		{"git@code.example.com:team/app.git", "No ED25519 host key is known for code.example.com and you have requested strict checking.\nHost key verification failed.\nfatal: Could not read from remote repository.", "host_key_unverified"},
		{"ssh://git@code.example.com:2222/team/app.git", "@@@ WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED! @@@\nHost key verification failed.", "host_key_unverified"},
		{"git@code.example.com:team/app.git", "git@code.example.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", "auth_failed"},
		{"https://code.example.com/team/app.git", "fatal: could not read Username for 'https://code.example.com': terminal prompts disabled", "auth_failed"},
		{"https://github.com/o/r.git", "remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/o/r.git/'", "auth_failed"},
		{"https://gitlab.example.com/o/r.git", "remote: HTTP Basic: Access denied\nfatal: Authentication failed", "auth_failed"},
		{"https://e.com/o/r.git", "fatal: unable to access 'https://e.com/o/r.git/': The requested URL returned error: 403", "auth_failed"},
		{"https://e.com/o/r.git", "warning: Could not find remote branch nope to clone.\nfatal: Remote branch nope not found in upstream origin", "branch_not_found"},
		{"https://e.com/o/r.git", "fatal: unable to access 'https://e.com/o/r.git/': Could not resolve host: e.com", "clone_failed"},
		{"https://github.com/o/r.git", "remote: Repository not found.\nfatal: repository 'https://github.com/o/r.git/' not found", "clone_failed"},
	}
	for _, c := range cases {
		code, msg := classifyCloneFailure(c.url, c.out)
		if code != c.code {
			t.Errorf("%q → %s，应为 %s", c.out, code, c.code)
		}
		if !strings.Contains(msg, strings.TrimSpace(strings.Split(c.out, "\n")[0])) {
			t.Errorf("消息应带 git 输出：%q", msg)
		}
	}
	_, msg := classifyCloneFailure("ssh://git@code.example.com:2222/team/app.git", "Host key verification failed.")
	if !strings.Contains(msg, "ssh -T -p 2222 git@code.example.com") || !strings.Contains(msg, "known_hosts") {
		t.Errorf("主机密钥失败应告诉用户怎么确认：%q", msg)
	}
}

func TestSSHTarget(t *testing.T) {
	for in, want := range map[string]string{
		"git@code.example.com:team/app.git":             "git@code.example.com",
		"code.example.com:team/app.git":                 "code.example.com",
		"ssh://git@code.example.com/team/app.git":       "git@code.example.com",
		"ssh://code.example.com:2222/app.git":           "-p 2222 code.example.com",
		"https://code.example.com/team/app.git":         "",
		"file:///tmp/repo":                              "",
		"/tmp/repo":                                     "",
		"git+ssh://deploy@code.example.com/app.git":     "deploy@code.example.com",
		"ssh://git@[2001:db8::1]:22/app.git":            "-p 22 git@2001:db8::1",
		"git@code.example.com:/abs/path/with:colon.git": "git@code.example.com",
	} {
		if got := sshTarget(in); got != want {
			t.Errorf("sshTarget(%q) = %q，应为 %q", in, got, want)
		}
	}
}
