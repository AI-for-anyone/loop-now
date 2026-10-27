package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// ProtectSecrets applies only to this invocation's final JSON, including server
// errors and warnings. Never include credential values in diagnostics or storage.
func (inv *Invocation) ProtectSecrets(values ...string) {
	for _, value := range values {
		if value != "" {
			inv.secretOutput.values = append(inv.secretOutput.values, value)
		}
	}
}

type secretOutput struct {
	io.Writer
	values []string
}

func (w *secretOutput) Write(b []byte) (int, error) {
	if len(w.values) == 0 {
		return w.Writer.Write(b)
	}
	var data any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return 0, err
	}
	var redact func(any) any
	clean := func(s string) string {
		for _, value := range w.values {
			s = strings.ReplaceAll(s, value, "[REDACTED]")
		}
		return s
	}
	redact = func(v any) any {
		switch x := v.(type) {
		case string:
			return clean(x)
		case []any:
			for i, v := range x {
				x[i] = redact(v)
			}
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, v := range x {
				out[clean(k)] = redact(v)
			}
			return out
		}
		return v
	}
	safe, err := json.Marshal(redact(data))
	if err != nil {
		return 0, err
	}
	safe = append(safe, '\n')
	if _, err = w.Writer.Write(safe); err != nil {
		return 0, err
	}
	return len(b), nil
}
