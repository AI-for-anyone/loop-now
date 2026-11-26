package convimport

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

// codexRolloutID is the thread id at the end of a rollout's name (rollout-<time>-<thread id>.jsonl); a rewound thread's
// rollout ends in <thread id>_<rollout id>.
var codexRolloutID = regexp.MustCompile(`-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:_[0-9a-f-]{36})?\.jsonl$`)

// codexFiles lists Codex's rollouts under ~/.codex/sessions/<year>/<month>/<day>/ (compressed older ones, .jsonl.zst,
// are left out).
func codexFiles(root string) []candidate {
	var out []candidate
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		id := ""
		if m := codexRolloutID.FindStringSubmatch(d.Name()); m != nil {
			id = m[1]
		}
		out = append(out, candidate{cli: CLICodex, path: path, id: id, size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	return out
}

// codexNames reads Codex's session index (one line per rename: {id, thread_name}); the newest name wins.
func codexNames(o ScanOptions, path string) map[string]string {
	names := map[string]string{}
	_, _ = eachRecord(o, path, false, func(line []byte) bool {
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(line, &e) == nil && e.ID != "" && strings.TrimSpace(e.ThreadName) != "" {
			names[e.ID] = strings.TrimSpace(e.ThreadName)
		}
		return true
	})
	return names
}

// codexRecord is what is used of one Codex rollout record.
type codexRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		ID      string          `json:"id"`
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Cwd     string          `json:"cwd"`
		Model   string          `json:"model"`
		Message string          `json:"message"`
		Content json.RawMessage `json:"content"`
		Source  json.RawMessage `json:"source"`
		TurnID  string          `json:"turn_id"`
	} `json:"payload"`
}

// codexPreamble is the setting-up text Codex records as user text ahead of the person's request.
var codexPreamble = regexp.MustCompile(`^(?:\s*(?:<(recommended_plugins|environment_context|user_instructions|INSTRUCTIONS|in-app-browser-context|external_codex_apps_open_page)(?:\s[^>]*)?>[\s\S]*?</(?:recommended_plugins|environment_context|user_instructions|INSTRUCTIONS|in-app-browser-context|external_codex_apps_open_page)>|# AGENTS\.md instructions\b[^\n]*|## My request for Codex:))+`)

// codexReader reads a Codex rollout. The person's prompts are its user_message events; its replies are assistant
// messages. A rollout without user_message events (older Codex) takes its user messages, without Codex's setting-up
// text.
type codexReader struct {
	m         meta
	events    bool
	pending   []Message // user messages from response items, used only when no user_message event comes
	onMessage func(Message)
	// lastTurn is the last turn that ended (finished or stopped), and messagesAtTurn how many messages came up to then:
	// a fork from that turn holds those messages.
	lastTurn       string
	messagesAtTurn int
}

func (r *codexReader) record(line []byte) bool {
	var rec codexRecord
	if json.Unmarshal(line, &rec) != nil {
		return true
	}
	at := parseTime(rec.Timestamp)
	p := rec.Payload
	switch {
	case rec.Type == "session_meta":
		if isSubagentSource(p.Source) {
			r.m.skip = true
			return false
		}
		if p.ID != "" && r.m.sessionID == "" {
			r.m.sessionID = p.ID
		}
		if p.Cwd != "" && r.m.cwd == "" {
			r.m.cwd = p.Cwd
		}
		return true
	case rec.Type == "turn_context":
		if strings.TrimSpace(p.Model) != "" {
			r.m.model = strings.TrimSpace(p.Model)
		}
		if p.Cwd != "" && r.m.cwd == "" {
			r.m.cwd = p.Cwd
		}
	case rec.Type == "event_msg" && (p.Type == "task_complete" || p.Type == "turn_aborted") && p.TurnID != "":
		r.lastTurn, r.messagesAtTurn = p.TurnID, r.m.messages
	case rec.Type == "event_msg" && p.Type == "user_message":
		if !r.events {
			r.events, r.pending = true, nil
		}
		r.message(Message{Role: "user", Text: strings.TrimSpace(p.Message), At: at})
	case rec.Type == "response_item" && p.Type == "message" && p.Role == "assistant":
		r.message(Message{Role: "assistant", Text: contentText(p.Content), At: at})
	case rec.Type == "response_item" && p.Type == "message" && p.Role == "user" && !r.events:
		text := strings.TrimSpace(codexPreamble.ReplaceAllString(contentText(p.Content), ""))
		if text != "" {
			r.pending = append(r.pending, Message{Role: "user", Text: text, At: at})
		}
		return true
	}
	if !at.IsZero() && at.After(r.m.lastAt) && rec.Type != "session_meta" {
		r.m.lastAt = at
	}
	return true
}

func (r *codexReader) message(msg Message) {
	if msg.Text == "" {
		return
	}
	if !r.events && msg.Role == "assistant" {
		// No user_message events so far: the user messages before this reply are the prompts.
		for _, p := range r.pending {
			r.take(p)
		}
		r.pending = nil
	}
	r.take(msg)
}

func (r *codexReader) take(msg Message) {
	if !msg.At.IsZero() && msg.At.After(r.m.lastAt) {
		r.m.lastAt = msg.At
	}
	r.m.messages++
	if msg.Role == "user" {
		r.m.prompt(msg.Text)
	}
	if msg.Role == "user" && r.m.firstPrompt == "" {
		r.m.firstPrompt, r.m.titlePrompt = clip(msg.Text, promptLimit), msg.Text
	}
	if r.onMessage != nil {
		r.onMessage(msg)
	}
}

func (r *codexReader) result() meta {
	if !r.events {
		for _, p := range r.pending {
			r.take(p)
		}
		r.pending = nil
	}
	return r.m
}

// isSubagentSource reports whether a rollout's session_meta source is a sub-agent's.
func isSubagentSource(raw json.RawMessage) bool {
	var src map[string]json.RawMessage
	if json.Unmarshal(raw, &src) != nil {
		return false
	}
	_, a := src["subagent"]
	_, b := src["subAgent"]
	return a || b
}
