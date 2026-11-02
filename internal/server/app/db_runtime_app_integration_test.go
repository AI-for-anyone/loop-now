package app_test

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"loop-now/internal/server/admincli"
)

// The production installer reexecs os.Executable. In this test executable only,
// delegate that hidden entry to the real production admin dispatcher; normal
// tests and the actual loop-server binary use their original entry points.
func init() {
	if len(os.Args) > 1 && (os.Args[1] == "internal-db-cli" || os.Args[1] == "internal-gcloud-cli") {
		os.Exit(admincli.Run(context.Background(), os.Args[1:], admincli.Options{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr}))
	}
}

type dbLoopbackTransport struct {
	base     http.RoundTripper
	url      string
	archives map[string]string
}

func (t dbLoopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	key, ok := t.archives[r.URL.String()]
	if !ok {
		return nil, fmt.Errorf("unexpected tool download rejected")
	}
	clone := r.Clone(r.Context())
	parsed, _ := http.NewRequest("GET", t.url+"/"+key, nil)
	clone.URL = parsed.URL
	clone.Host = parsed.URL.Host
	return t.base.RoundTrip(clone)
}
