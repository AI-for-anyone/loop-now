package convimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// claudeFiles lists Claude Code's conversation records: ~/.claude/projects/<folder>/<session id>.jsonl (sub-agents'
// records sit in deeper folders and are left out).
func claudeFiles(root string) []candidate {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []candidate
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			out = append(out, candidate{cli: CLIClaude, path: filepath.Join(root, d.Name(), e.Name()), id: strings.TrimSuffix(e.Name(), ".jsonl"),
				size: info.Size(), mtime: info.ModTime()})
		}
	}
	return out
}

// claudeRecord is what is used of one Claude Code record.
type claudeRecord struct {
	Type             string  `json:"type"`
	SessionID        string  `json:"sessionId"`
	Cwd              string  `json:"cwd"`
	Timestamp        string  `json:"timestamp"`
	IsSidechain      bool    `json:"isSidechain"`
	IsMeta           bool    `json:"isMeta"`
	IsCompactSummary bool    `json:"isCompactSummary"`
	AITitle          *string `json:"aiTitle"`
	CustomTitle      *string `json:"customTitle"`
	Summary          string  `json:"summary"`
	Message          *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// claudeSyntheticModel is the model Claude Code names its own local errors with ("API Error"): not a model to continue
// with.
const claudeSyntheticModel = "<synthetic>"

// Message is a prompt or a reply of a conversation, as shown: text only.
type Message struct {
	Role string // user or assistant
	Text string
	At   time.Time
}

// claudeReader reads a Claude Code session: its fields, and each visible message to onMessage when set.
type claudeReader struct {
	m           meta
	aiTitle     string
	customTitle *string
	summary     string
	onMessage   func(Message)
}

func (r *claudeReader) record(line []byte) bool {
	var rec claudeRecord
	if json.Unmarshal(line, &rec) != nil {
		return true
	}
	if rec.Summary != "" && rec.Type == "summary" {
		r.summary = rec.Summary
	}
	if rec.IsSidechain || rec.IsMeta || rec.IsCompactSummary {
		return true
	}
	if rec.SessionID != "" && r.m.sessionID == "" {
		r.m.sessionID = rec.SessionID
	}
	if rec.Cwd != "" && r.m.cwd == "" {
		r.m.cwd = rec.Cwd
	}
	if rec.AITitle != nil {
		if t := claudeTitle(*rec.AITitle); t != "" {
			r.aiTitle = t
		}
	}
	if rec.CustomTitle != nil {
		t := claudeTitle(*rec.CustomTitle)
		r.customTitle = &t // an empty rename clears it
	}
	if (rec.Type != "user" && rec.Type != "assistant") || rec.Message == nil {
		return true
	}
	at := parseTime(rec.Timestamp)
	if !at.IsZero() && at.After(r.m.lastAt) {
		r.m.lastAt = at
	}
	text := contentText(rec.Message.Content)
	titleText := text
	if rec.Type == "user" {
		text, titleText = claudeUserText(text)
	} else if model := strings.TrimSpace(rec.Message.Model); model != "" && model != claudeSyntheticModel {
		r.m.model = model
	}
	if text == "" {
		return true
	}
	r.m.messages++
	if rec.Type == "user" && r.m.firstPrompt == "" && strings.TrimSpace(titleText) != "" {
		r.m.firstPrompt, r.m.titlePrompt = clip(text, promptLimit), titleText
	}
	if r.onMessage != nil {
		r.onMessage(Message{Role: rec.Type, Text: text, At: at})
	}
	return true
}

func (r *claudeReader) result() meta {
	m := r.m
	switch {
	case r.customTitle != nil && *r.customTitle != "":
		m.title = *r.customTitle
	case r.aiTitle != "":
		m.title = r.aiTitle
	case r.summary != "":
		m.title = titleOf(r.summary)
	}
	return m
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// contentText is a message's text: a string, or its text blocks (text, input_text, output_text) joined.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" || b.Type == "input_text" || b.Type == "output_text" {
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

var (
	claudeUIOutput    = regexp.MustCompile(`^(?:\s*<(local-command-stdout|local-command-stderr|local-command-caveat|bash-stdout|bash-stderr)>[\s\S]*?</(?:local-command-stdout|local-command-stderr|local-command-caveat|bash-stdout|bash-stderr)>\s*)+`)
	claudeCommandPart = regexp.MustCompile(`^(?:\s*<command-(?:name|message|args)>[\s\S]*?</command-(?:name|message|args)>\s*)+$`)
	claudeCommandName = regexp.MustCompile(`<command-name>([^<]*)</command-name>`)
	claudeCommandMsg  = regexp.MustCompile(`<command-message>([^<]*)</command-message>`)
	claudeCommandArgs = regexp.MustCompile(`<command-args>([\s\S]*?)</command-args>`)
	claudeShellInput  = regexp.MustCompile(`^<bash-input>([\s\S]*)</bash-input>$`)
	claudeInterrupted = regexp.MustCompile(`^\[Request interrupted by user(?: for tool use)?\]$`)
)

// claudeUserText is what a Claude Code user record shows the person typed, and the part that may name the
// conversation. Claude Code records its own UI as user records too: command output, shell output, task notifications,
// interruptions show nothing; a slash command shows as typed (/name args), a shell run as !command.
func claudeUserText(text string) (shown, titleText string) {
	if text == "" || strings.HasPrefix(text, "<task-notification>") || claudeInterrupted.MatchString(text) {
		return "", ""
	}
	if loc := claudeUIOutput.FindStringIndex(text); loc != nil {
		if loc[1] == len(text) {
			return "", ""
		}
		text = strings.TrimSpace(text[loc[1]:])
	}
	if m := claudeShellInput.FindStringSubmatch(text); m != nil {
		return "!" + m[1], ""
	}
	if claudeCommandPart.MatchString(text) {
		name := ""
		if m := claudeCommandName.FindStringSubmatch(text); m != nil {
			name = strings.TrimSpace(m[1])
		}
		if name == "" {
			if m := claudeCommandMsg.FindStringSubmatch(text); m != nil {
				name = "/" + strings.TrimSpace(m[1])
			}
		}
		args := ""
		if m := claudeCommandArgs.FindStringSubmatch(text); m != nil {
			args = strings.TrimSpace(m[1])
		}
		if args == "" {
			return name, ""
		}
		return name + " " + args, args
	}
	return text, text
}

// claudeTitle is a stored title, unless it is the raw markup of a user record (a command that opened the session).
func claudeTitle(title string) string {
	title = strings.TrimSpace(title)
	if shown, _ := claudeUserText(title); shown != title {
		return ""
	}
	return title
}
