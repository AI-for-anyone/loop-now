package httpapi

import (
	"net/http"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/devices"
	"loop-now/internal/server/remoteaccess"
	"loop-now/internal/server/store"
)

func remoteAccessOff(message string) error {
	return apperr.Conflict(apperr.CodeRemoteAccessOff, message)
}

func deviceJSON(d store.DeviceSession) apitypes.Device {
	return apitypes.Device{ID: d.ID, Name: d.Name, CreatedAt: d.CreatedAt.UTC(), LastSeenAt: d.LastSeenAt.UTC(), LastAddress: d.LastAddress,
		ExpiresAt: devices.ExpiresAt(d).UTC()}
}

func (s *Server) getRemoteAccess(w http.ResponseWriter, r *http.Request) {
	if s.RemoteAccess == nil {
		writeJSON(w, http.StatusOK, remoteaccess.State{State: remoteaccess.StateOff})
		return
	}
	writeJSON(w, http.StatusOK, s.RemoteAccess.State())
}

func (s *Server) putRemoteAccess(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
		Port    *int  `json:"port"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Enabled == nil {
		s.fail(w, r, apperr.Invalid("enabled", "Required: true or false."))
		return
	}
	if in.Port != nil && (*in.Port < 1 || *in.Port > 65535) {
		s.fail(w, r, apperr.Invalid("port", "Must be between 1 and 65535, or null to let the server pick one."))
		return
	}
	if s.RemoteAccess == nil {
		s.fail(w, r, remoteAccessOff("This server has no remote door."))
		return
	}
	st, err := s.RemoteAccess.Apply(r.Context(), *in.Enabled, in.Port)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) createDevicePairing(w http.ResponseWriter, r *http.Request) {
	if s.RemoteAccess == nil || s.Devices == nil {
		s.fail(w, r, remoteAccessOff("This server has no remote door."))
		return
	}
	st := s.RemoteAccess.State()
	if st.State != remoteaccess.StateListening || st.URL == nil {
		s.fail(w, r, remoteAccessOff("Turn on access from phones and other devices first."))
		return
	}
	p, err := s.Devices.NewPairing()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.DevicePairing{Code: p.Code, URL: *st.URL + "/pair#" + p.Code, ExpiresAt: p.ExpiresAt.UTC()})
}

func (s *Server) createDeviceSession(w http.ResponseWriter, r *http.Request) {
	if doorOf(r) != DoorRemote {
		s.fail(w, r, apperr.Forbidden("Devices pair at the remote door; this computer needs no pairing."))
		return
	}
	var in struct {
		Code *string `json:"code"`
		Name *string `json:"name"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Code == nil || *in.Code == "" || len(*in.Code) > 256 {
		s.fail(w, r, apperr.Invalid("code", "Required: the pairing code from the link."))
		return
	}
	name := ""
	if in.Name != nil {
		var err error
		if name, err = devices.CleanName("name", *in.Name); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if s.Devices == nil {
		s.fail(w, r, remoteAccessOff("This server has no remote door."))
		return
	}
	d, token, err := s.Devices.Pair(r.Context(), *in.Code, name, r.UserAgent(), clientAddress(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// A stale cookie the request carried was told to be forgotten; the new session replaces that.
	w.Header().Del("Set-Cookie")
	http.SetCookie(w, devices.Cookie(token, requestIsHTTPS(r)))
	writeJSON(w, http.StatusCreated, deviceJSON(d))
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	out := apitypes.DeviceList{Devices: []apitypes.Device{}}
	if s.Devices != nil {
		list, err := s.Devices.List(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
		for _, d := range list {
			out.Devices = append(out.Devices, deviceJSON(d))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "deviceId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		Name *string `json:"name"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Name == nil {
		s.fail(w, r, apperr.Invalid("name", "Required: a name for the device."))
		return
	}
	if s.Devices == nil {
		s.fail(w, r, apperr.NotFound("Device"))
		return
	}
	d, err := s.Devices.Rename(r.Context(), id, *in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, deviceJSON(d))
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "deviceId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Devices == nil {
		s.fail(w, r, apperr.NotFound("Device"))
		return
	}
	if err := s.Devices.Remove(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
