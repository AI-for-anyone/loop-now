package httpapi

import (
	"net/http"
	goruntime "runtime"
	"strconv"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/runtimes"
)

// runtimeDefinition mirrors the RuntimeDefinition schema.
type runtimeDefinition struct {
	ID           string                    `json:"id"`
	Name         string                    `json:"name"`
	Providers    []string                  `json:"providers"`
	IsDefault    bool                      `json:"isDefault"`
	Capabilities agentruntime.Capabilities `json:"capabilities"`
	Setup        *agentruntime.Setup       `json:"setup"`
	MinVersion   *string                   `json:"minVersion"`
	AcpCommandID *uuid.UUID                `json:"acpCommandId"`
}

// listRuntimeDefinitions returns the fixed definition of each runtime from the registry. It starts no process and
// checks nothing on this computer.
func (s *Server) listRuntimeDefinitions(w http.ResponseWriter, _ *http.Request) {
	list := []runtimeDefinition{}
	for _, rt := range agentruntime.All() {
		providers := rt.Providers
		if providers == nil {
			providers = []string{}
		}
		def := runtimeDefinition{ID: rt.ID, Name: rt.Name, Providers: providers, IsDefault: rt.IsDefault(), Capabilities: rt.Capabilities,
			Setup: rt.SetupOn(goruntime.GOOS)}
		if rt.MinVersion != "" {
			def.MinVersion = &rt.MinVersion
		}
		if rt.Command != nil {
			id := rt.CommandID
			def.AcpCommandID = &id
		}
		list = append(list, def)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": list})
}

// listRuntimes reports whether each Agent runtime is available on this machine; refresh=true bypasses the cache.
func (s *Server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	refresh := false
	if v := r.URL.Query().Get("refresh"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			s.fail(w, r, apperr.Invalid("refresh", "Must be true or false."))
			return
		}
		refresh = b
	}
	switch {
	case refresh && s.RefreshLocalModels != nil:
		s.RefreshLocalModels(r.Context())
	case !refresh && s.ModelListRead != nil:
		// The local CLIs' model lists, and with them Grok's and Antigravity's login conclusions, are read again in the
		// background when they have grown old; this read does not wait.
		s.ModelListRead("")
	}
	list := []runtimes.Status{}
	if s.Runtimes != nil {
		list = s.Runtimes.List(r.Context(), refresh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"runtimes": list})
}
