package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/devices"
	"loop-now/internal/server/remoteaccess"
)

// Door is the listener a request came in on. The listener decides, never the request's headers.
type Door string

const (
	DoorLocal    Door = "local"    // the loopback console listener: the person at this computer, no login
	DoorRemote   Door = "remote"   // the remote door: paired devices, through a tunnel
	DoorExternal Door = "external" // the external TLS port: external identities
)

type doorKey struct{}

func withDoor(d Door, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), doorKey{}, d)))
	})
}

// doorOf returns the listener a request came in on; local when none was recorded (tests that call handlers directly).
func doorOf(r *http.Request) Door {
	if d, ok := r.Context().Value(doorKey{}).(Door); ok {
		return d
	}
	return DoorLocal
}

// RemoteAccessController runs the remote door's listener (remoteaccess.Door).
type RemoteAccessController interface {
	State() remoteaccess.State
	Apply(ctx context.Context, enabled bool, port *int) (remoteaccess.State, error)
}

// RemoteHandler returns the handler for the remote door: the same console files and /api/v1 as the local door, but
// every API request needs a paired device's session except the pairing operations, the local-only operations are
// refused, and requests whose Origin is not the remote door's own are refused. /agent/v1 and the plugins' /hooks are
// not served.
func (s *Server) RemoteHandler() http.Handler {
	root := http.NewServeMux()
	root.Handle("/api/v1/", rawHeaders(s.deviceAuth(s.newMux("/api/v1", s.Routes()))))
	ingress := s.deviceAuth(requireDevice(s.pluginIngress("console")))
	root.Handle("/api/v1/plugins/{pluginId}/http", ingress)
	root.Handle("/api/v1/plugins/{pluginId}/http/{path...}", ingress)
	root.Handle("GET /healthz", s.deviceAuth(requireDevice(http.HandlerFunc(s.getHealth))))
	notServed := func(w http.ResponseWriter, r *http.Request) { writeError(w, unknownEndpoint()) }
	root.HandleFunc("/agent/v1/", notServed)
	root.HandleFunc("/hooks/", notServed)
	if s.O.Console != nil {
		root.Handle("/", s.O.Console)
	}
	return withCommonHeaders(s.logRequests(withDoor(DoorRemote, s.remoteOrigin(root))))
}

func isWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// remoteOrigin refuses requests whose Origin is not one of the remote door's own, `Origin: null` included, and writes
// without an Origin. Browsers send Origin on every write and on cross-origin reads, so a page from another site, a
// sibling subdomain or a sandboxed file view cannot act as the console here even when its requests carry the session
// cookie. The door's own origins come from what it knows, never from the request: its loopback address, and the
// tunnel's https://<hostname> as the relay gave it (a tunnel may or may not pass the public Host on).
func (s *Server) remoteOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		switch {
		case origin != "" && !s.remoteOwnOrigin(r, origin):
			writeError(w, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodeForbiddenOrigin, Message: "The remote door accepts requests only from its own pages."})
			return
		case origin == "" && isWrite(r.Method):
			writeError(w, &apperr.Error{Status: http.StatusForbidden, Code: apperr.CodeForbiddenOrigin, Message: "Changes at the remote door must come from its own pages (the request has no Origin)."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) remoteOwnOrigin(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if s.Relay != nil {
		if own := s.Relay.PublicOrigin(); own != "" && strings.EqualFold(origin, own) {
			return true
		}
	}
	if u.Scheme != "http" {
		return false
	}
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok {
		return false
	}
	port := strconv.Itoa(addr.Port)
	return strings.EqualFold(u.Host, net.JoinHostPort(remoteaccess.Host, port)) || strings.EqualFold(u.Host, net.JoinHostPort("localhost", port))
}

func deviceRequired() error {
	return apperr.Unauthenticated("This device is not paired, or it was removed or not used for 30 days. Pair it again from Settings on the computer.")
}

// deviceAuth reads the device session cookie at the remote door. A valid session makes the request the console
// operator's, from that device; the request is cancelled when the device is removed. Without one the request goes on
// with no device, and the route decides (only pairing needs none). Authorization headers are refused: the remote
// door takes no tokens.
func (s *Server) deviceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := auth.BearerToken(r); present {
			writeError(w, apperr.Forbidden("The remote door accepts no tokens. Pair the device from Settings on the computer."))
			return
		}
		p := auth.Console()
		seen := map[string]bool{}
		for _, c := range r.Cookies() {
			if c.Name != devices.CookieName && c.Name != devices.SecureCookieName {
				continue
			}
			seen[c.Name] = true
			ctx, done, err := s.deviceSession(r, c.Value)
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if ctx == nil {
				continue
			}
			defer done()
			p.Device = ctx.Value(deviceKey{}).(uuid.UUID)
			r = r.WithContext(ctx)
			break
		}
		if p.Device == uuid.Nil {
			// Tell the browser to forget a session that no longer works.
			for name := range seen {
				http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: name == devices.SecureCookieName, SameSite: http.SameSiteStrictMode})
			}
		}
		next.ServeHTTP(w, r.WithContext(auth.With(r.Context(), p)))
	})
}

type deviceKey struct{}

// deviceSession checks one session token. On success it returns the request context, cancelled when the device is
// removed, and done to call when the request ends; a token that is not a live session returns a nil context.
func (s *Server) deviceSession(r *http.Request, token string) (context.Context, func(), error) {
	if s.Devices == nil {
		return nil, nil, nil
	}
	id, ok := devices.TokenID(token)
	if !ok {
		return nil, nil, nil
	}
	ctx, cancel := context.WithCancel(r.Context())
	untrack := s.Devices.Track(id, cancel)
	done := func() { untrack(); cancel() }
	d, err := s.Devices.Authenticate(ctx, token, clientAddress(r))
	if err != nil {
		done()
		if apperr.Is(err, apperr.CodeUnauthenticated) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return context.WithValue(ctx, deviceKey{}, d.ID), done, nil
}

// requireDevice refuses requests without a device session (for the remote door's entries outside the route table).
func requireDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, _ := auth.From(r.Context()); p.Device == uuid.Nil {
			writeError(w, deviceRequired())
			return
		}
		next.ServeHTTP(w, r)
	})
}

// remoteRefusal returns why the remote door refuses a route for this request, or nil: local-only routes always, the
// others without a device session, except pairing.
func remoteRefusal(r *http.Request, rt Route, p auth.Principal) error {
	if doorOf(r) != DoorRemote {
		return nil
	}
	if rt.Access == LocalOnly {
		return apperr.Forbidden("Only the person at the computer can do this. Open Loop Now on the computer.")
	}
	if rt.Access != Pairing && p.Device == uuid.Nil {
		return deviceRequired()
	}
	return nil
}

// clientAddress is where a request came from, for showing in the device list only: what the tunnel reports
// (CF-Connecting-IP, else the first X-Forwarded-For entry), else the connection's own address.
func clientAddress(r *http.Request) *string {
	for _, v := range []string{r.Header.Get("CF-Connecting-IP"), strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0], r.RemoteAddr} {
		v = strings.TrimSpace(v)
		if host, _, err := net.SplitHostPort(v); err == nil {
			v = host
		}
		if ip := net.ParseIP(v); ip != nil {
			s := ip.String()
			return &s
		}
	}
	return nil
}

// requestIsHTTPS reports whether the page a request came from is HTTPS: the request came over TLS, its Origin is
// https, or the tunnel says so. It only decides whether the session cookie is Secure, which a forged header cannot
// turn against anyone but the sender.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil || strings.HasPrefix(strings.ToLower(r.Header.Get("Origin")), "https://") {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
