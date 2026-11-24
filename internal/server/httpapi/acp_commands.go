package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// acpCommand mirrors the AcpCommand schema; the environment's values are never returned.
type acpCommand struct {
	ID        uuid.UUID `json:"id"`
	Runtime   string    `json:"runtime"`
	Name      string    `json:"name"`
	Command   string    `json:"command"`
	Args      []string  `json:"args"`
	EnvNames  []string  `json:"envNames"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func acpCommandOf(c store.ACPCommand) acpCommand {
	return acpCommand{ID: c.ID, Runtime: c.Runtime(), Name: c.Name, Command: c.Command, Args: c.Args, EnvNames: c.EnvNames(), CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

func (s *Server) listACPCommands(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ACPCommands(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]acpCommand, 0, len(list))
	for _, c := range list {
		out = append(out, acpCommandOf(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"commands": out})
}

func (s *Server) createACPCommand(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string            `json:"name"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := checkACPCommand(&in.Name, &in.Command, in.Args, keysOf(in.Env)); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Store.CreateACPCommand(r.Context(), in.Name, in.Command, in.Args, in.Env)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ApplyACPCommands(r.Context())
	writeJSON(w, http.StatusCreated, acpCommandOf(c))
}

func (s *Server) updateACPCommand(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "commandId")
	if err != nil {
		s.fail(w, r, apperr.NotFound("ACP command"))
		return
	}
	var in struct {
		Name    *string            `json:"name"`
		Command *string            `json:"command"`
		Args    *[]string          `json:"args"`
		Env     map[string]*string `json:"env"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Name == nil && in.Command == nil && in.Args == nil && in.Env == nil {
		s.fail(w, r, apperr.Invalid("body", "Give at least one field to change."))
		return
	}
	if in.Name != nil {
		trimmed := strings.TrimSpace(*in.Name)
		in.Name = &trimmed
	}
	var args []string
	if in.Args != nil {
		args = *in.Args
	}
	if err := checkACPCommand(in.Name, in.Command, args, keysOf(in.Env)); err != nil {
		s.fail(w, r, err)
		return
	}
	c, err := s.Store.UpdateACPCommand(r.Context(), id, store.ACPCommandPatch{Name: in.Name, Command: in.Command, Args: in.Args, Env: in.Env})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.ApplyACPCommands(r.Context())
	writeJSON(w, http.StatusOK, acpCommandOf(c))
}

func (s *Server) deleteACPCommand(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "commandId")
	if err != nil {
		s.fail(w, r, apperr.NotFound("ACP command"))
		return
	}
	if err := s.Store.DeleteACPCommand(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	s.ApplyACPCommands(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// checkACPCommand checks the fields given (nil or empty means not given): a name that is not blank, an executable
// that is a plain name or an absolute path, arguments without NUL, and environment names the platform does not
// reserve.
func checkACPCommand(name, command *string, args, envNames []string) error {
	if name != nil && *name == "" {
		return apperr.Invalid("name", "Must not be blank.")
	}
	if command != nil {
		c := *command
		if strings.TrimSpace(c) == "" || strings.ContainsRune(c, 0) || (strings.ContainsAny(c, `/\`) && !isAbsPath(c)) {
			return apperr.Invalid("command", "Must be a program name on the login shell's PATH or an absolute path.")
		}
	}
	for _, a := range args {
		if strings.ContainsRune(a, 0) {
			return apperr.Invalid("args", "Arguments must not contain NUL.")
		}
	}
	for _, k := range envNames {
		if !envNamePattern.MatchString(k) {
			return apperr.Invalid("env", "Variable names are letters, digits and _, not starting with a digit: "+k)
		}
		if executor.IsReservedEnv(k) {
			return apperr.Invalid("env", "The platform sets this variable itself: "+k)
		}
	}
	return nil
}

func isAbsPath(p string) bool {
	return strings.HasPrefix(p, "/") || (len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ApplyACPCommands makes the saved local ACP commands the runtimes the platform runs: the registry, the model catalog
// and the runtime status follow at once. The server calls it at startup and after each change; a failure to read them
// leaves the runtimes as they were.
func (s *Server) ApplyACPCommands(ctx context.Context) {
	list, err := s.Store.ACPCommands(ctx)
	if err != nil {
		if s.Log != nil {
			s.Log.Warn("could not read the local ACP commands", "err", err)
		}
		return
	}
	rts := make([]agentruntime.Runtime, 0, len(list))
	for _, c := range list {
		rts = append(rts, agentruntime.LocalCommandRuntime(c.ID, c.Name, agentruntime.LocalCommand{Command: c.Command, Args: c.Args, Env: c.Env}))
	}
	agentruntime.SetLocalCommands(rts)
	if m, ok := s.Models.(interface{ RuntimesChanged() }); ok {
		m.RuntimesChanged()
	}
	if s.Runtimes != nil {
		s.Runtimes.Forget()
	}
}
