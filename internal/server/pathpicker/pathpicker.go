// Package pathpicker shows this computer's own dialog to choose a folder or file and returns its full path, for the
// console in a browser (POST /path-picks), which never learns a full path. The console port listens only on this
// computer, so the dialog shows on the screen the browser is on. The desktop app has its own dialog
// (desktop/src/pick-path.ts); this one follows the same rules. Only macOS for now.
package pathpicker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"loop-now/internal/server/apperr"
)

// Kind is what the dialog chooses.
type Kind string

const (
	Directory Kind = "directory"
	File      Kind = "file"
)

// MaxPathLength (characters) is longer than any path the systems accept; the desktop app uses the same limit.
const MaxPathLength = 4096

// Request is what the console asks for; DefaultPath is usually the field's current value.
type Request struct {
	Kind        Kind
	DefaultPath string
}

// Runner runs a program and returns what it printed. It must stop the program as soon as ctx ends.
type Runner func(ctx context.Context, name string, args []string) (stdout, stderr []byte, err error)

const (
	defaultOSAScript = "/usr/bin/osascript"
	launchctl        = "/bin/launchctl"
	plutil           = "/usr/bin/plutil"
)

// Picker shows the dialog, one at a time. The zero value works on this computer; it must not be copied.
type Picker struct {
	GOOS      string // the system; runtime.GOOS when empty
	OSAScript string // /usr/bin/osascript when empty
	Run       Runner // runs the programs; execRun when nil
	Log       *slog.Logger

	once   sync.Once
	reason string // why the dialog can't be shown here; empty when it can
	busy   atomic.Bool
}

// dialogScript (JavaScript for Automation) shows the system's open panel: argv[0] is the kind, argv[1] where to open
// (a folder). A folder dialog has the New Folder button; a file dialog shows hidden files, since programs often live
// in hidden places (~/.local/bin) or ones Finder hides (/opt/homebrew), and treats an .app as one file. osascript is a
// background process that macOS (since 14) does not let take the focus from the browser, so the panel is put at the
// modal panel level (8), where AppleScript's own `choose folder` puts it: above every app's normal windows, taking the
// keyboard once clicked. Nothing is sent to another app, so macOS asks for no Automation permission. It prints
// {"path": <chosen path or null when cancelled>} as ASCII JSON, so the output's encoding never matters. The bridge
// returns Objective-C integers as strings ("1"), hence Number(...).
const dialogScript = `ObjC.import('AppKit');
function run(argv) {
	var directory = argv[0] === 'directory';
	$.NSApplication.sharedApplication;
	var panel = $.NSOpenPanel.openPanel;
	panel.canChooseDirectories = directory;
	panel.canChooseFiles = !directory;
	panel.canCreateDirectories = directory;
	panel.showsHiddenFiles = !directory;
	panel.treatsFilePackagesAsDirectories = false;
	panel.allowsMultipleSelection = false;
	if (argv.length > 1) panel.directoryURL = $.NSURL.fileURLWithPathIsDirectory(argv[1], true);
	panel.level = 8;
	var path = Number(panel.runModal) === 1 ? panel.URL.path.js : null;
	return JSON.stringify({ path: path }).replace(/[\u007f-\uffff]/g, function (c) {
		return '\\u' + ('000' + c.charCodeAt(0).toString(16)).slice(-4);
	});
}`

// Available reports whether this computer can show the dialog (Context.pathPicker). The answer is worked out once.
func (p *Picker) Available() bool { return p.unavailable() == "" }

func (p *Picker) unavailable() string {
	p.once.Do(func() { p.reason = p.check() })
	return p.reason
}

// check says why the dialog can't be shown here: not macOS, no osascript, or no logged-in desktop session (a server
// started over SSH or as a system service can't show windows; launchctl calls the desktop session "Aqua").
func (p *Picker) check() string {
	if p.goos() != "darwin" {
		return "Choosing a path in a dialog works only on macOS for now; type the path instead."
	}
	if st, err := os.Stat(p.osascript()); err != nil || st.IsDir() {
		return fmt.Sprintf("osascript (%s) is missing, so this Mac can't show the dialog; type the path instead.", p.osascript())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, stderr, err := p.run(ctx, launchctl, []string{"managername"})
	if err != nil {
		return fmt.Sprintf("Could not tell whether the server runs in a logged-in desktop session (launchctl managername: %s); type the path instead.", reason(stderr, err))
	}
	if name := strings.TrimSpace(string(out)); name != "Aqua" {
		return fmt.Sprintf("The server does not run in a logged-in desktop session (launchctl managername says %q), so it can't show a dialog; type the path instead.", name)
	}
	return ""
}

// Pick shows the dialog and returns the chosen path, or nil when the person cancelled. It waits for the person
// without a time limit; when ctx ends the dialog closes at once and ctx's error is returned.
func (p *Picker) Pick(ctx context.Context, req Request) (*string, error) {
	if req.Kind != Directory && req.Kind != File {
		return nil, apperr.Invalid("kind", "Must be directory or file.")
	}
	if utf8.RuneCountInString(req.DefaultPath) > MaxPathLength {
		return nil, apperr.Invalid("defaultPath", fmt.Sprintf("At most %d characters.", MaxPathLength))
	}
	if strings.ContainsRune(req.DefaultPath, 0) {
		return nil, apperr.Invalid("defaultPath", "Must not contain a NUL character.")
	}
	if why := p.unavailable(); why != "" {
		return nil, unavailable(why)
	}
	if !p.busy.CompareAndSwap(false, true) {
		return nil, apperr.Conflict(apperr.CodePathPickerBusy, "A dialog to choose a path is already open; choose or cancel there first.")
	}
	defer p.busy.Store(false)

	args := []string{"-l", "JavaScript", "-e", dialogScript, string(req.Kind)}
	if dir := openAt(req.DefaultPath); dir != "" {
		args = append(args, dir)
	}
	out, stderr, err := p.run(ctx, p.osascript(), args)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		why := reason(stderr, err)
		p.log().Warn("path picker: the dialog failed", "error", why)
		return nil, unavailable("The dialog could not be shown: " + why)
	}
	var answer struct {
		Path json.RawMessage `json:"path"`
	}
	var path *string
	if json.Unmarshal(out, &answer) != nil || len(answer.Path) == 0 || json.Unmarshal(answer.Path, &path) != nil || path != nil && *path == "" {
		p.log().Warn("path picker: the dialog's answer can't be read", "bytes", len(out))
		return nil, unavailable("The dialog gave an answer that can't be read.")
	}
	if path != nil && req.Kind == File {
		resolved := p.appExecutable(ctx, *path)
		path = &resolved
	}
	return path, nil
}

// openAt is where the dialog opens for defaultPath: the folder itself, or a file's folder, when without surrounding
// spaces it is an absolute path that exists; empty otherwise, and the dialog opens where it usually does.
func openAt(defaultPath string) string {
	path := strings.TrimSpace(defaultPath)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st.IsDir() {
		return filepath.Clean(path)
	}
	return filepath.Dir(path)
}

var appSuffix = regexp.MustCompile(`(?i)\.app$`)

// appExecutable: the macOS file dialog treats an .app bundle as one file, so choosing a program there gives the
// bundle, which can't be run as a command. A chosen .app stands for the program inside it:
// Contents/MacOS/<CFBundleExecutable from Contents/Info.plist>. When that can't be read, names a path outside
// Contents/MacOS, or isn't there, the path is kept as chosen (the server then says it can't run it). Anything else, and
// any path on another system, is kept as is. A port of appExecutable in desktop/src/pick-path.ts.
func (p *Picker) appExecutable(ctx context.Context, path string) string {
	app := strings.TrimRight(path, "/")
	if p.goos() != "darwin" || !appSuffix.MatchString(app) {
		return path
	}
	name, ok := p.bundleExecutable(ctx, filepath.Join(app, "Contents", "Info.plist"))
	if !ok || name == "" || strings.Contains(name, "/") || name == "." || name == ".." {
		return path
	}
	executable := filepath.Join(app, "Contents", "MacOS", name)
	if st, err := os.Stat(executable); err != nil || !st.Mode().IsRegular() {
		return path
	}
	return executable
}

var bundleExecutableKey = regexp.MustCompile(`<key>\s*CFBundleExecutable\s*</key>\s*<string>([^<]*)</string>`)

// maxPlist bounds how much of an Info.plist is read; real ones are a few kilobytes.
const maxPlist = 1 << 20

// bundleExecutable reads CFBundleExecutable from an Info.plist: an XML one directly; a binary one (bplist00) is turned
// into XML by the system's plutil.
func (p *Picker) bundleExecutable(ctx context.Context, plist string) (string, bool) {
	f, err := os.Open(plist)
	if err != nil {
		return "", false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPlist))
	f.Close()
	if err != nil {
		return "", false
	}
	if bytes.HasPrefix(data, []byte("bplist00")) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if data, _, err = p.run(ctx, plutil, []string{"-convert", "xml1", "-o", "-", plist}); err != nil {
			return "", false
		}
	}
	m := bundleExecutableKey.FindSubmatch(data)
	if m == nil {
		return "", false
	}
	return decodeXML(strings.TrimSpace(string(m[1]))), true
}

var (
	xmlEntity   = regexp.MustCompile(`(?i)&(#x[0-9a-f]+|#\d+|lt|gt|quot|apos|amp);`)
	xmlEntities = map[string]string{"lt": "<", "gt": ">", "quot": `"`, "apos": "'", "amp": "&"}
)

// decodeXML turns XML's character references and the five named entities back into characters; anything else is
// kept as written.
func decodeXML(text string) string {
	return xmlEntity.ReplaceAllStringFunc(text, func(whole string) string {
		entity := whole[1 : len(whole)-1]
		if entity[0] != '#' {
			if v, ok := xmlEntities[strings.ToLower(entity)]; ok {
				return v
			}
			return whole
		}
		var code uint64
		var err error
		if entity[1] == 'x' || entity[1] == 'X' {
			code, err = strconv.ParseUint(entity[2:], 16, 32)
		} else {
			code, err = strconv.ParseUint(entity[1:], 10, 32)
		}
		if err != nil || code == 0 || code > utf8.MaxRune {
			return whole
		}
		return string(rune(code))
	})
}

func unavailable(message string) *apperr.Error {
	return apperr.Conflict(apperr.CodePathPickerUnavailable, message)
}

// reason is the last line a program printed on stderr, or its error when it printed nothing.
func reason(stderr []byte, err error) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return err.Error()
	}
	if r := []rune(last); len(r) > 300 {
		last = string(r[:300]) + "…"
	}
	return last
}

func (p *Picker) goos() string {
	if p.GOOS != "" {
		return p.GOOS
	}
	return runtime.GOOS
}

func (p *Picker) osascript() string {
	if p.OSAScript != "" {
		return p.OSAScript
	}
	return defaultOSAScript
}

func (p *Picker) run(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	if p.Run != nil {
		return p.Run(ctx, name, args)
	}
	return execRun(ctx, name, args)
}

func (p *Picker) log() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// execRun runs a program; when ctx ends the program is killed at once (an open dialog closes with it).
func execRun(ctx context.Context, name string, args []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
