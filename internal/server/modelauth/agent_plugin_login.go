package modelauth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/providerstate"
	"loop-now/internal/server/store"
)

// A platform Agent's own plugin-provider login (plugins/SPEC.md §13). The account belongs to the Agent: a successful
// login adds a version to this Agent's model auth versions whose "credential" only names the plugin; tokens and the
// extension's state live in a state directory named after the version ID. The login runs exactly like a project
// account login (same helper process, same URL checks). The platform operation table has no device-code columns, so
// the device code, like the pending question, lives in memory only (a restart ends logins in progress with
// server_restarted).

// plugins returns what plugin-provider logins need, shared with project accounts; nil when not assembled.
func (s *Service) plugins() *PluginLogins {
	if s.Project == nil {
		return nil
	}
	return s.Project.Plugins
}

// checkPluginLogin runs before a platform Agent's plugin-provider login starts: the login components must be
// assembled and the plugin granted to this Agent.
func (s *Service) checkPluginLogin(ctx context.Context, agentID uuid.UUID, plugin string) error {
	l := s.plugins()
	if l == nil || l.Helper == nil || l.States == nil || l.Extensions == nil {
		return ErrHelperUnavailable
	}
	reason, err := s.Store.AgentPluginUsability(ctx, agentID, plugin)
	if err != nil {
		return err
	}
	if reason != "" {
		return apperr.Conflict(apperr.CodePluginNotEnabled, "Plugin "+plugin+" is not granted to this Agent. Grant it first, then log in.")
	}
	return nil
}

// view is the console view of an operation. Codex is unchanged. For a plugin provider the authorization URL is
// checked with the generic plugin rules, error messages come from the plugin login set, and while the login runs the
// in-memory device code and pending question are added.
func (s *Service) view(op store.ModelAuthOperation) Operation {
	o := operationView(op)
	if op.Provider == "openai-codex" {
		return o
	}
	o.AuthorizationURL = op.AuthorizationURL
	if terminal(op.Status) || o.AuthorizationURL != nil && !ValidPluginURL(*o.AuthorizationURL) {
		o.AuthorizationURL = nil
	}
	if op.ErrorCode != nil {
		if m, ok := pluginErrorMessages[*op.ErrorCode]; ok {
			o.ErrorMessage = &m
		}
	}
	if terminal(op.Status) {
		return o
	}
	if st := s.plugins().state(op.ID); st != nil {
		st.mu.Lock()
		if st.prompt != nil {
			q := *st.prompt
			o.Prompt = &q
		}
		if st.device != nil {
			d := *st.device
			o.DeviceCode = &d
		}
		st.mu.Unlock()
	}
	return o
}

type agentPluginUI struct {
	s     *Service
	op    store.ModelAuthOperation
	state *pluginLoginState
}

func (u agentPluginUI) record() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), u.s.Options.RecordTimeout)
}

func (u agentPluginUI) AuthURL(url string) error {
	ctx, stop := u.record()
	defer stop()
	next, err := u.s.Store.SetModelAuthOperationURL(ctx, u.op.ID, url)
	if err != nil {
		return err
	}
	if terminal(next.Status) {
		return ErrAuthCancelled
	}
	return nil
}

func (u agentPluginUI) DeviceCode(code, uri string, expiresIn time.Duration) error {
	ctx, stop := u.record()
	defer stop()
	next, err := u.s.Store.MarkModelAuthOperationAwaiting(ctx, u.op.ID)
	if err != nil {
		return err
	}
	if terminal(next.Status) {
		return ErrAuthCancelled
	}
	d := cm.DeviceCode{UserCode: code, VerificationURI: uri}
	if expiresIn > 0 {
		t := time.Now().UTC().Add(expiresIn)
		d.ExpiresAt = &t
	}
	u.state.mu.Lock()
	u.state.device = &d
	u.state.mu.Unlock()
	return nil
}

func (u agentPluginUI) Prompt(q cm.LoginPrompt) { u.state.setPrompt(q) }

func (u agentPluginUI) PromptClosed(id string) { u.state.closePrompt(id) }

// pluginLogin runs one plugin-provider login for a platform Agent: new state directory, helper login, write
// auth.json and rename to the final directory, commit the version.
func (s *Service) pluginLogin(ctx context.Context, op store.ModelAuthOperation, work *loginWorker, plugin string) {
	defer s.workers.Done()
	defer work.cancel()
	l := s.plugins()
	state := l.register(op.ID)
	defer func() {
		l.unregister(op.ID)
		s.mu.Lock()
		delete(s.operations, op.ID)
		s.mu.Unlock()
		close(work.done)
	}()
	version := uuid.Must(uuid.NewV7())
	stage := "plugin_extensions"
	var err error
	var extensions []string
	if extensions, err = l.Extensions(ctx, plugin); err == nil {
		stage = "state_directory"
		var home string
		if home, err = l.States.Begin(version); err == nil {
			stage = "helper_login"
			var entry []byte
			entry, err = l.Helper.Login(ctx, PluginLoginRequest{OperationID: op.ID, Provider: op.Provider, Extensions: extensions, Home: home}, agentPluginUI{s: s, op: op, state: state}, state.replies)
			if err == nil && ctx.Err() == nil {
				stage = "credential_commit"
				err = s.publishPluginLogin(op, plugin, version, entry)
				clear(entry)
				if err == nil || errors.Is(err, providerstate.ErrNotSaved) {
					return
				}
			} else if err == nil {
				err = ErrAuthCancelled
			}
		}
	} else if store.IsNotFound(err) {
		err = errPluginUnavailable
	}
	l.States.Discard(version)
	status, code := pluginLoginFailure(ctx, err)
	if s.Log != nil {
		s.Log.Warn("plugin provider login did not complete", "agentId", op.AgentID, "operationId", op.ID, "provider", op.Provider, "plugin", plugin, "code", code, "stage", stage, "detail", helperDiagnostic(err))
	}
	record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
	defer stop()
	if _, err := s.Store.EndModelAuthOperation(record, op.ID, status, code); err != nil && s.Log != nil {
		s.Log.Warn("failed to save the model login status", "agentId", op.AgentID, "operationId", op.ID, "code", "auth_unavailable")
	}
}

// publishPluginLogin turns the state directory of a successful login into this Agent's version, under the same
// lock as the database commit.
func (s *Service) publishPluginLogin(op store.ModelAuthOperation, plugin string, version uuid.UUID, entry []byte) error {
	credential, err := modelcredential.PluginStateOf(plugin)
	if err != nil {
		return err
	}
	plain, err := credential.Encode()
	if err != nil {
		return err
	}
	defer clear(plain)
	saved := false
	err = s.plugins().States.Publish(version, op.Provider, entry, func() error {
		record, stop := context.WithTimeout(context.Background(), s.Options.RecordTimeout)
		defer stop()
		_, v, err := s.Store.CompletePluginModelAuthOperation(record, op.ID, version, plain)
		if err != nil {
			return err
		}
		if v == nil {
			return providerstate.ErrNotSaved
		}
		saved = true
		return nil
	})
	if err == nil && saved && s.Changed != nil {
		s.Changed(op.AgentID)
	}
	return err
}

// Input hands the person's answer for a platform Agent login to the helper process. Only the pending question can be
// answered; the answer is passed in memory only, never stored or logged.
func (s *Service) Input(ctx context.Context, agentID, id uuid.UUID, in cm.LoginInput) (State, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return State{}, err
	}
	o, err := s.Store.GetModelAuthOperation(ctx, agentID, id)
	if err != nil {
		return State{}, err
	}
	if len(in.Value) > 4096 {
		return State{}, apperr.Invalid("value", "Must be at most 4096 bytes.")
	}
	st := s.plugins().state(id)
	if terminal(o.Status) || st == nil {
		return State{}, apperr.Invalid("promptId", "This login is not waiting for an answer.")
	}
	if err := st.answer(in); err != nil {
		return State{}, err
	}
	return s.State(ctx, agentID)
}
