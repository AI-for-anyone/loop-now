package claudecli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"
)

// TestEncodeProjectDir covers the encoding of session transcript subdirectory names: every non-alphanumeric character
// becomes -, including the Windows drive letter's colon, backslashes and the ~ in 8.3 short names.
func TestEncodeProjectDir(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\RUNNER~1\AppData\Local\Temp\T1\agents\0192\workspace`: "C--Users-RUNNER-1-AppData-Local-Temp-T1-agents-0192-workspace",
		`C:\Users\runneradmin\work`:                                     "C--Users-runneradmin-work",
		"/private/tmp/claude-501/a b/c.d":                               "-private-tmp-claude-501-a-b-c-d",
	} {
		if got := EncodeProjectDir(in); got != want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestProjectDirNames covers the candidate subdirectory names: the resolved one first, the one as given second; when
// resolving fails or gives the same result, there is only the one as given.
func TestProjectDirNames(t *testing.T) {
	short := `C:\Users\RUNNER~1\w`
	long := func(string) (string, error) { return `C:\Users\runneradmin\w`, nil }
	if got := projectDirNames(short, long); !slices.Equal(got, []string{"C--Users-runneradmin-w", "C--Users-RUNNER-1-w"}) {
		t.Fatalf("short name expansion: %v", got)
	}
	fail := func(string) (string, error) { return "", errors.New("no") }
	if got := projectDirNames(short, fail); !slices.Equal(got, []string{"C--Users-RUNNER-1-w"}) {
		t.Fatalf("resolving fails: %v", got)
	}
	same := func(p string) (string, error) { return p, nil }
	if got := projectDirNames("/a/b", same); !slices.Equal(got, []string{"-a-b"}) {
		t.Fatalf("only one name when both are the same: %v", got)
	}
}

// TestTranscriptPathFallsBackToRawName checks that the session transcript is still found when claude encodes the path
// as given (on macOS the temp directory is under /var, which resolves to /private/var, so the two candidates differ).
func TestTranscriptPathFallsBackToRawName(t *testing.T) {
	projects, cwd := t.TempDir(), t.TempDir()
	names := ProjectDirNames(cwd)
	if len(names) < 2 {
		t.Skip("the temp directory here involves no symlink, so the two candidates are the same")
	}
	id := uuid.NewString()
	p := filepath.Join(projects, names[1], id+".jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte("{}\n"), 0o600)
	if got := TranscriptPath(projects, cwd, id); got != p {
		t.Fatalf("subdirectory name as given: %q, want %q", got, p)
	}
}
