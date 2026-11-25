package pathpicker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"loop-now/internal/server/apperr"
)

// fakeRunner answers each program by name and records the calls.
type fakeRunner struct {
	mu      sync.Mutex
	calls   [][]string
	answers map[string]func(ctx context.Context, args []string) ([]byte, []byte, error)
}

func (f *fakeRunner) run(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	answer := f.answers[filepath.Base(name)]
	f.mu.Unlock()
	if answer == nil {
		return nil, []byte("not faked"), errors.New("exit status 1")
	}
	return answer(ctx, args)
}

func (f *fakeRunner) called(name string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if filepath.Base(c[0]) == name {
			out = append(out, c)
		}
	}
	return out
}

func printed(v any) func(context.Context, []string) ([]byte, []byte, error) {
	return func(context.Context, []string) ([]byte, []byte, error) {
		b, _ := json.Marshal(v)
		return append(b, '\n'), nil, nil
	}
}

// macPicker is a picker on a Mac in a desktop session, whose dialog answers with dialog.
func macPicker(t *testing.T, dialog func(context.Context, []string) ([]byte, []byte, error)) (*Picker, *fakeRunner) {
	t.Helper()
	osascript := filepath.Join(t.TempDir(), "osascript")
	if err := os.WriteFile(osascript, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{answers: map[string]func(context.Context, []string) ([]byte, []byte, error){
		"launchctl": func(context.Context, []string) ([]byte, []byte, error) { return []byte("Aqua\n"), nil, nil },
		"osascript": dialog,
	}}
	return &Picker{GOOS: "darwin", OSAScript: osascript, Run: f.run}, f
}

func wantError(t *testing.T, err error, status int, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Status != status || e.Code != code {
		t.Fatalf("want %d %s, got %v", status, code, err)
	}
	return e
}

// The dialog works on macOS in a desktop session with osascript; anywhere else it is unavailable with the reason,
// and the session is asked once.
func TestAvailable(t *testing.T) {
	p, f := macPicker(t, nil)
	if !p.Available() || !p.Available() {
		t.Fatal("a Mac in a desktop session: want available")
	}
	if calls := f.called("launchctl"); len(calls) != 1 || strings.Join(calls[0][1:], " ") != "managername" {
		t.Fatalf("launchctl calls: %v", calls)
	}

	cases := map[string]func(p *Picker, f *fakeRunner){
		"macOS": func(p *Picker, _ *fakeRunner) { p.GOOS = "linux" },
		"osascript": func(p *Picker, _ *fakeRunner) {
			p.OSAScript = filepath.Join(filepath.Dir(p.OSAScript), "missing")
		},
		"desktop session": func(_ *Picker, f *fakeRunner) {
			f.answers["launchctl"] = func(context.Context, []string) ([]byte, []byte, error) { return []byte("Background\n"), nil, nil }
		},
		"launchctl": func(_ *Picker, f *fakeRunner) { delete(f.answers, "launchctl") },
	}
	for want, change := range cases {
		p, f := macPicker(t, printed(map[string]any{"path": "/x"}))
		change(p, f)
		if p.Available() {
			t.Fatalf("%s: want unavailable", want)
		}
		_, err := p.Pick(context.Background(), Request{Kind: Directory})
		if e := wantError(t, err, http.StatusConflict, apperr.CodePathPickerUnavailable); !strings.Contains(e.Message, want) {
			t.Fatalf("%s: message %q", want, e.Message)
		}
		if len(f.called("osascript")) != 0 {
			t.Fatalf("%s: the dialog ran", want)
		}
	}
}

// A folder dialog runs osascript with the kind and where to open; the answer is the chosen path, and null when
// cancelled. defaultPath counts only when it is an absolute path that exists; a file opens its folder.
func TestPickDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, f := macPicker(t, printed(map[string]any{"path": "/Users/a/项目 b"})) // check-english: zh (a non-ASCII path)
	cases := []struct{ defaultPath, location string }{
		{"", ""},
		{dir, dir},
		{"  " + dir + "\n", dir},
		{file, dir},
		{filepath.Join(dir, "missing"), ""},
		{"relative/path", ""},
		{"claude", ""},
	}
	for _, c := range cases {
		got, err := p.Pick(context.Background(), Request{Kind: Directory, DefaultPath: c.defaultPath})
		if err != nil || got == nil || *got != "/Users/a/项目 b" { // check-english: zh (a non-ASCII path)
			t.Fatalf("%q: %v %v", c.defaultPath, got, err)
		}
		calls := f.called("osascript")
		args := calls[len(calls)-1][1:]
		if len(args) < 4 || args[0] != "-l" || args[1] != "JavaScript" || args[2] != "-e" || args[3] != dialogScript {
			t.Fatalf("osascript args: %q", args)
		}
		want := []string{"directory"}
		if c.location != "" {
			want = append(want, c.location)
		}
		if strings.Join(args[4:], "|") != strings.Join(want, "|") {
			t.Fatalf("%q: script args %q, want %q", c.defaultPath, args[4:], want)
		}
	}

	p, _ = macPicker(t, printed(map[string]any{"path": nil}))
	if got, err := p.Pick(context.Background(), Request{Kind: Directory}); err != nil || got != nil {
		t.Fatalf("cancelled: %v %v", got, err)
	}
}

// The script's answer is ASCII JSON, with \u escapes for everything else.
func TestPickDecodesEscapes(t *testing.T) {
	p, _ := macPicker(t, func(context.Context, []string) ([]byte, []byte, error) {
		return []byte(`{"path":"/Users/a/\u9879\u76ee/\ud83d\ude00 \"q\"\nline"}` + "\n"), nil, nil
	})
	got, err := p.Pick(context.Background(), Request{Kind: Directory})
	if err != nil || got == nil || *got != "/Users/a/项目/😀 \"q\"\nline" { // check-english: zh (a non-ASCII path)
		t.Fatalf("%q %v", deref(got), err)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// A dialog that fails to open, or answers something else, is unavailable with osascript's own reason.
func TestPickFailure(t *testing.T) {
	p, _ := macPicker(t, func(context.Context, []string) ([]byte, []byte, error) {
		return nil, []byte("execution error: No user interaction allowed. (-1713)\n"), errors.New("exit status 1")
	})
	_, err := p.Pick(context.Background(), Request{Kind: File})
	if e := wantError(t, err, http.StatusConflict, apperr.CodePathPickerUnavailable); !strings.Contains(e.Message, "No user interaction allowed. (-1713)") {
		t.Fatalf("message %q", e.Message)
	}
	for _, out := range []string{"", "garbage\n", `{"path":""}` + "\n"} {
		p, _ := macPicker(t, func(context.Context, []string) ([]byte, []byte, error) { return []byte(out), nil, nil })
		_, err := p.Pick(context.Background(), Request{Kind: File})
		wantError(t, err, http.StatusConflict, apperr.CodePathPickerUnavailable)
	}
}

// One dialog at a time: while one is open, another request is busy; once it is answered, the next one opens.
func TestPickBusy(t *testing.T) {
	opened, release := make(chan struct{}), make(chan struct{})
	first := true
	p, _ := macPicker(t, func(context.Context, []string) ([]byte, []byte, error) {
		if first {
			first = false
			close(opened)
			<-release
		}
		return []byte(`{"path":"/a"}`), nil, nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := p.Pick(context.Background(), Request{Kind: Directory})
		done <- err
	}()
	<-opened
	_, err := p.Pick(context.Background(), Request{Kind: File})
	wantError(t, err, http.StatusConflict, apperr.CodePathPickerBusy)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, err := p.Pick(context.Background(), Request{Kind: File}); err != nil || deref(got) != "/a" {
		t.Fatalf("after the first: %q %v", deref(got), err)
	}
}

// When the request goes away the dialog stops at once and the picker is free again.
func TestPickStopsWithTheRequest(t *testing.T) {
	p, _ := macPicker(t, func(ctx context.Context, _ []string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, errors.New("signal: killed")
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, err := p.Pick(ctx, Request{Kind: Directory}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	p2, _ := macPicker(t, printed(map[string]any{"path": "/b"}))
	p.Run = p2.Run
	if got, err := p.Pick(context.Background(), Request{Kind: Directory}); err != nil || deref(got) != "/b" {
		t.Fatalf("after a stop: %q %v", deref(got), err)
	}
}

// The real runner kills the program as soon as the context ends.
func TestExecRunKills(t *testing.T) {
	if os.Getenv("PATHPICKER_HELPER") == "sleep" {
		time.Sleep(time.Minute)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Setenv("PATHPICKER_HELPER", "sleep")
	start := time.Now()
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	_, _, err := execRun(ctx, os.Args[0], []string{"-test.run=^TestExecRunKills$"})
	if err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	out, _, err := execRun(context.Background(), os.Args[0], []string{"-test.run=^$"})
	if err != nil || !strings.Contains(string(out), "ok") && !strings.Contains(string(out), "PASS") {
		t.Fatalf("a normal run: %q %v", out, err)
	}
}

// The request is checked before anything opens.
func TestPickValidation(t *testing.T) {
	p, f := macPicker(t, printed(map[string]any{"path": "/a"}))
	for _, c := range []struct {
		req   Request
		field string
	}{
		{Request{Kind: "folder"}, "kind"},
		{Request{Kind: ""}, "kind"},
		{Request{Kind: Directory, DefaultPath: "/a\x00b"}, "defaultPath"},
		{Request{Kind: File, DefaultPath: "/" + strings.Repeat("a", MaxPathLength)}, "defaultPath"},
	} {
		_, err := p.Pick(context.Background(), c.req)
		if e := wantError(t, err, http.StatusUnprocessableEntity, apperr.CodeValidationFailed); len(e.Fields) != 1 || e.Fields[0].Field != c.field {
			t.Fatalf("%+v: %+v", c.req, e.Fields)
		}
	}
	if len(f.called("osascript")) != 0 {
		t.Fatal("the dialog ran")
	}
}

// writeApp makes name.app with an Info.plist (raw text) and, when exe is not empty, Contents/MacOS/exe.
func writeApp(t *testing.T, dir, name, plist, exe string) string {
	t.Helper()
	app := filepath.Join(dir, name+".app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if exe != "" {
		if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", exe), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func xmlPlist(executable string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
	<key>CFBundleName</key><string>Tool</string>
	<key>CFBundleExecutable</key>
	<string> %s </string>
</dict></plist>`, executable)
}

// A chosen .app stands for the program inside it, read from an XML or (through plutil) a binary Info.plist; when
// that can't be read, names a path outside Contents/MacOS or isn't there, the path is kept as chosen. Folders and
// other systems are kept as is.
func TestAppExecutable(t *testing.T) {
	dir := t.TempDir()
	xml := writeApp(t, dir, "Xml Tool", xmlPlist("Xml Tool"), "Xml Tool")
	entities := writeApp(t, dir, "Amp", xmlPlist("A&amp;B&#x43;&#68;&lt;"), "A&BCD<")
	binary := writeApp(t, dir, "Bin", "bplist00\x00\x01binary", "bin-exe")
	missing := writeApp(t, dir, "Missing", xmlPlist("gone"), "")
	escape := writeApp(t, dir, "Escape", xmlPlist("../../x"), "")
	noKey := writeApp(t, dir, "NoKey", `<plist><dict><key>CFBundleName</key><string>x</string></dict></plist>`, "x")
	upper := writeApp(t, dir, "Upper", xmlPlist("up"), "up")
	upper2 := filepath.Join(dir, "Upper.APP")
	if err := os.Rename(upper, upper2); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(dir, "plain.app")
	if err := os.WriteFile(plain, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	f := &fakeRunner{answers: map[string]func(context.Context, []string) ([]byte, []byte, error){
		"plutil": func(_ context.Context, args []string) ([]byte, []byte, error) {
			want := []string{"-convert", "xml1", "-o", "-", filepath.Join(binary, "Contents", "Info.plist")}
			if strings.Join(args, "|") != strings.Join(want, "|") {
				return nil, []byte("bad args"), errors.New("exit status 1")
			}
			return []byte(xmlPlist("bin-exe")), nil, nil
		},
	}}
	p := &Picker{GOOS: "darwin", Run: f.run}
	for path, want := range map[string]string{
		xml:                     filepath.Join(xml, "Contents", "MacOS", "Xml Tool"),
		xml + "/":               filepath.Join(xml, "Contents", "MacOS", "Xml Tool"),
		entities:                filepath.Join(entities, "Contents", "MacOS", "A&BCD<"),
		binary:                  filepath.Join(binary, "Contents", "MacOS", "bin-exe"),
		upper2:                  filepath.Join(upper2, "Contents", "MacOS", "up"),
		missing:                 missing,
		escape:                  escape,
		noKey:                   noKey,
		plain:                   plain,
		filepath.Join(dir, "x"): filepath.Join(dir, "x"),
	} {
		if got := p.appExecutable(context.Background(), path); got != want {
			t.Fatalf("%s: got %s, want %s", path, got, want)
		}
	}
	if calls := f.called("plutil"); len(calls) != 1 || calls[0][0] != "/usr/bin/plutil" {
		t.Fatalf("plutil calls: %v", calls)
	}

	other := &Picker{GOOS: "linux", Run: f.run}
	if got := other.appExecutable(context.Background(), xml); got != xml {
		t.Fatalf("not macOS: %s", got)
	}

	// Through Pick: only a chosen file is resolved.
	p, _ = macPicker(t, printed(map[string]any{"path": xml}))
	if got, err := p.Pick(context.Background(), Request{Kind: File}); err != nil || deref(got) != filepath.Join(xml, "Contents", "MacOS", "Xml Tool") {
		t.Fatalf("file: %q %v", deref(got), err)
	}
	if got, err := p.Pick(context.Background(), Request{Kind: Directory}); err != nil || deref(got) != xml {
		t.Fatalf("folder: %q %v", deref(got), err)
	}
}

// On this Mac the script compiles; the dialog itself is never opened by tests.
func TestDialogScriptCompiles(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if _, err := os.Stat("/usr/bin/osacompile"); err != nil {
		t.Skip("no osacompile")
	}
	out := filepath.Join(t.TempDir(), "dialog.scpt")
	if b, err := exec.Command("/usr/bin/osacompile", "-l", "JavaScript", "-e", dialogScript, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("osacompile: %v %s", err, b)
	}
}

// The real dialog, end to end; it opens a window, so it runs only with LOOP_NOW_TEST_DIALOG=1 on macOS. A timer in
// the script ends the panel after a second as OK (1) or Cancel (0), the codes its buttons give; nothing is clicked. It
// checks what the fakes can't: the panel's answer as the bridge returns it (a string), and the printed path.
func TestRealDialog(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("LOOP_NOW_TEST_DIALOG") != "1" {
		t.Skip("set LOOP_NOW_TEST_DIALOG=1 on macOS to open the real dialog")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "项目 a") // check-english: zh (a non-ASCII path)
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	const answer = "\tvar path = Number(panel.runModal)"
	if !strings.Contains(dialogScript, answer) {
		t.Fatal("the script changed; update this test")
	}
	for _, c := range []struct {
		code string
		want *string
	}{{"1", &folder}, {"0", nil}} {
		end := "\tvar timer = $.NSTimer.timerWithTimeIntervalRepeatsBlock(1, false, function () { $.NSApp.stopModalWithCode(" + c.code + "); });\n" +
			"\t$.NSRunLoop.currentRunLoop.addTimerForMode(timer, 'NSModalPanelRunLoopMode');\n"
		p := &Picker{Run: func(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
			if filepath.Base(name) == "osascript" {
				args = append([]string(nil), args...)
				args[3] = strings.Replace(args[3], answer, end+answer, 1)
			}
			return execRun(ctx, name, args)
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		got, err := p.Pick(ctx, Request{Kind: Directory, DefaultPath: folder})
		cancel()
		if err != nil || (c.want == nil) != (got == nil) || c.want != nil && *got != *c.want {
			t.Fatalf("code %s: got %q %v, want %q", c.code, deref(got), err, deref(c.want))
		}
	}
}
