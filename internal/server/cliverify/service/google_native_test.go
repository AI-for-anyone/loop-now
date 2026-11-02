package service

import (
	"loop-now/internal/server/cliverify"
	"net/http"
)

func ioWrite(w http.ResponseWriter, s string) { _, _ = w.Write([]byte(s)) }
func safeCode(r cliverify.Result) string {
	if r.ErrorCode == nil {
		return "none"
	}
	return *r.ErrorCode
}

func serverRedirectTarget() string { return "https://forbidden.invalid/never-follow" }
