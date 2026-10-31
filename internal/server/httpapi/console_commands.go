package httpapi

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
)

func platformResult(command string) apitypes.ConsoleCommandResult {
	return apitypes.ConsoleCommandResult{Handled: true, Source: "platform", Command: command}
}

// platformConsoleCommand 只认领平台内置命令；其余斜杠命令继续交给插件或普通消息链。
func (s *Server) platformConsoleCommand(r *http.Request, agentID uuid.UUID, text string) (apitypes.ConsoleCommandResult, bool, error) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return apitypes.ConsoleCommandResult{}, false, nil
	}
	body := strings.TrimPrefix(trimmed, "/")
	name, args := body, ""
	if i := strings.IndexFunc(body, unicode.IsSpace); i >= 0 {
		name, args = body[:i], strings.TrimSpace(body[i:])
	}
	if name != "session" && name != "new" && name != "compact" && name != "reload" && name != "copy" && name != "export" && name != "resume" {
		return apitypes.ConsoleCommandResult{}, false, nil
	}
	out := platformResult(name)
	if name != "compact" && name != "export" && name != "resume" && args != "" {
		return out, true, apperr.Invalid("text", "/"+name+" 不接受参数")
	}
	switch name {
	case "session":
		status, err := s.Conv.Status(r.Context(), agentID)
		if err != nil {
			return out, true, err
		}
		agent, err := s.Store.GetAgent(r.Context(), agentID)
		if err != nil {
			return out, true, err
		}
		thinking, err := s.Models.ResolveThinking(agent.ModelProvider, agent.ModelID, agent.ThinkingLevel)
		if err != nil {
			return out, true, err
		}
		out.Session = &apitypes.ConsoleSessionStatus{Status: status, Model: apitypes.ModelRef{Provider: agent.ModelProvider, ModelID: agent.ModelID}, ThinkingLevel: thinking}
	case "new":
		sess, err := s.Conv.NewSession(r.Context(), agentID)
		if err != nil {
			return out, true, err
		}
		out.ResetSession = &sess
	case "reload":
		stopped, err := s.Conv.Reload(r.Context(), agentID)
		if err != nil {
			return out, true, err
		}
		out.ProcessStopped = &stopped
	case "compact":
		result, err := s.Conv.Compact(r.Context(), agentID, args)
		if err != nil {
			return out, true, err
		}
		out.TokensBefore = &result.TokensBefore
		out.EstimatedTokensAfter = &result.EstimatedTokensAfter
	case "copy":
		message, err := s.Conv.LatestReply(r.Context(), agentID)
		if err != nil {
			return out, true, err
		}
		if message != nil {
			out.CopyText = &message.Text
		}
	case "export":
		format := args
		if format == "" {
			format = "html"
		}
		if format != "html" && format != "jsonl" {
			return out, true, apperr.Invalid("text", "/export 只接受 html 或 jsonl")
		}
		out.ExportFormat = &format
	case "resume":
		if args == "" {
			sessions, err := s.Conv.ListResumeSessions(r.Context(), agentID)
			if err != nil {
				return out, true, err
			}
			out.ResumeSessions = make([]apitypes.ConsoleResumeSession, 0, len(sessions))
			for _, sess := range sessions {
				out.ResumeSessions = append(out.ResumeSessions, apitypes.ConsoleResumeSession{ID: sess.ID, Title: sess.Title, Preview: sess.Preview, UpdatedAt: sess.UpdatedAt, Current: sess.Current})
			}
		} else {
			sess, err := s.Conv.ResumeSession(r.Context(), agentID, args)
			if err != nil {
				return out, true, err
			}
			out.ResumedSession = &sess
		}
	}
	return out, true, nil
}

func (s *Server) exportSession(w http.ResponseWriter, r *http.Request) {
	agentID, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := s.Conv.Export(r.Context(), agentID, r.URL.Query().Get("format"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out.Data)))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": out.Filename}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Data)
}
