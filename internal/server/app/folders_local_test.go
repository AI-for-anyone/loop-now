package app

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestGitPullEnv(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ghConfig := "GH_CONFIG_DIR=" + filepath.Join(home, ".config", "gh")
	helper := "!'/cli/gh' auth git-credential"
	gh := []string{"GH_TOKEN=synthetic"}

	// git switch off: same as before, the Agent's own HOME; clear the local helpers first, then use only gh.
	off := gitPullEnv(home, "/usr/bin:/bin", gh, helper, false, nil)
	if !slices.Equal(off.Env, []string{"HOME=" + home, "PATH=/usr/bin:/bin", "GIT_TERMINAL_PROMPT=0", ghConfig, "GH_TOKEN=synthetic"}) ||
		!slices.Equal(off.Args, []string{"-c", "credential.helper=", "-c", "credential.helper=" + helper}) {
		t.Fatalf("when off: %+v", off)
	}
	if anon := gitPullEnv(home, "/usr/bin", nil, "", false, nil); !slices.Equal(anon.Args, []string{"-c", "credential.helper="}) {
		t.Fatalf("anonymous without gh: %+v", anon)
	}

	// git switch on: use the local environment without clearing the local helpers; gh is used only for github.com.
	local := []string{"HOME=/Users/me", "SSH_AUTH_SOCK=/tmp/agent.sock", "GIT_TERMINAL_PROMPT=0"}
	on := gitPullEnv(home, "/usr/bin", gh, helper, true, local)
	if !slices.Equal(on.Env, append(slices.Clone(local), ghConfig, "GH_TOKEN=synthetic")) ||
		!slices.Equal(on.Args, []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=" + helper}) {
		t.Fatalf("when on: %+v", on)
	}
	if plain := gitPullEnv(home, "/usr/bin", nil, "", true, local); len(plain.Args) != 0 || slices.Contains(plain.Env, "GH_TOKEN=synthetic") {
		t.Fatalf("when on and without gh, no helper arguments should be added: %+v", plain)
	}
	if &on.Env[0] == &local[0] {
		t.Fatal("should not modify the localEnv passed in")
	}
}
