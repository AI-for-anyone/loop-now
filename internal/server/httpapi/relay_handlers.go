package httpapi

import (
	"context"
	"net/http"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/relay"
	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/server/tunnel"
)

// RelayController is the Loop Now account and the tunnel for phones (tunnel.Manager).
type RelayController interface {
	AccountStatus() relay.Status
	StartSignIn() (relay.SignIn, error)
	CancelSignIn()
	SignOut(ctx context.Context)
	State() tunnel.State
	SetEnabled(ctx context.Context, on bool) (tunnel.State, error)
	Release(ctx context.Context) error
	// DoorChanged follows the remote door after its setting changed.
	DoorChanged(door remoteaccess.State)
	// PublicOrigin is https://<hostname> once this computer has a tunnel address; empty before.
	PublicOrigin() string
	// OnlineURL is the tunnel's address while it is online.
	OnlineURL() (string, bool)
}

func (s *Server) getRelayAccount(w http.ResponseWriter, r *http.Request) {
	if s.Relay == nil {
		writeJSON(w, http.StatusOK, relay.Status{RelayURL: relay.DefaultURL})
		return
	}
	writeJSON(w, http.StatusOK, s.Relay.AccountStatus())
}

func (s *Server) signOutRelayAccount(w http.ResponseWriter, r *http.Request) {
	if s.Relay != nil {
		s.Relay.SignOut(r.Context())
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) startRelaySignIn(w http.ResponseWriter, r *http.Request) {
	if s.Relay == nil {
		s.fail(w, r, apperr.Conflict(apperr.CodeRelaySignedOut, "This server cannot sign in to a Loop Now account."))
		return
	}
	in, err := s.Relay.StartSignIn()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, in)
}

func (s *Server) cancelRelaySignIn(w http.ResponseWriter, r *http.Request) {
	if s.Relay != nil {
		s.Relay.CancelSignIn()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getTunnel(w http.ResponseWriter, r *http.Request) {
	if s.Relay == nil {
		writeJSON(w, http.StatusOK, tunnel.State{State: tunnel.StateOff})
		return
	}
	writeJSON(w, http.StatusOK, s.Relay.State())
}

func (s *Server) putTunnel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Enabled == nil {
		s.fail(w, r, apperr.Invalid("enabled", "Required: true or false."))
		return
	}
	if s.Relay == nil {
		if *in.Enabled {
			s.fail(w, r, apperr.Conflict(apperr.CodeRelaySignedOut, "This server cannot sign in to a Loop Now account."))
			return
		}
		writeJSON(w, http.StatusOK, tunnel.State{State: tunnel.StateOff})
		return
	}
	st, err := s.Relay.SetEnabled(r.Context(), *in.Enabled)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) releaseTunnelAddress(w http.ResponseWriter, r *http.Request) {
	if s.Relay != nil {
		if err := s.Relay.Release(r.Context()); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getDevicePairing(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "pairingId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Devices == nil {
		s.fail(w, r, apperr.NotFound("Pairing code"))
		return
	}
	st, err := s.Devices.PairingStatus(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.DevicePairingStatus{ID: st.ID, State: st.State, ExpiresAt: st.ExpiresAt.UTC()}
	if st.Device != nil {
		d := deviceJSON(*st.Device)
		out.Device = &d
	}
	writeJSON(w, http.StatusOK, out)
}
