package app_test

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/modelcredential"
)

type credentialStartupHelper struct{ calls atomic.Int32 }

func (h *credentialStartupHelper) Login(context.Context, uuid.UUID, func(string) error) (modelcredential.Credential, error) {
	h.calls.Add(1)
	return modelcredential.Credential{}, modelauth.ErrHelperUnavailable
}
