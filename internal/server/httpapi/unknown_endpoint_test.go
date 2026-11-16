package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
)

// A request that matches no endpoint returns 404 not_found with details.reason unknown_endpoint on every surface
// (console, Agent API, external port), so clients can tell "this server has no such endpoint" from a missing
// resource without reading the message; a missing resource still returns not_found without details.
func TestUnknownEndpointDetails(t *testing.T) {
	e := newEnv(t)
	a := e.platformAgent("Unknown endpoint")
	_, secret := e.identity("unknown-endpoint")
	console, agent, external := e.console(), e.agentCaller(a.ID), e.external(secret)
	for name, c := range map[string]caller{"console": console, "agent": agent, "external": external} {
		// Unknown paths are not in the contract, so they are requested with a client that does not validate.
		c.client = http.DefaultClient
		for _, path := range []string{"/no-such-endpoint", "/agents/" + uuid.NewString() + "/no-such-endpoint"} {
			body := decode[apitypes.ErrorBody](t, c.expect(404, "GET", path, nil))
			details, _ := body.Error.Details.(map[string]any)
			if body.Error.Code != "not_found" || details["reason"] != apitypes.NotFoundUnknownEndpoint || len(details) != 1 {
				t.Fatalf("%s %s: %+v", name, path, body.Error)
			}
		}
	}
	missing := decode[apitypes.ErrorBody](t, e.expect(404, "GET", "/agents/"+uuid.NewString(), nil))
	if missing.Error.Code != "not_found" || missing.Error.Details != nil {
		t.Fatalf("a missing resource carries no details: %+v", missing.Error)
	}
}
