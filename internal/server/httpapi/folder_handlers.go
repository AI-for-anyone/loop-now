package httpapi

import (
	"mime"
	"net/http"
	"path"
	"strconv"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/folders"
)

// folderOf 从请求中解析要操作的文件夹（Agent 文件夹、项目或全局上下文文件夹）。
type folderOf func(r *http.Request) (folders.Folder, error)

func (s *Server) agentFolder(r *http.Request) (folders.Folder, error) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		return folders.Folder{}, err
	}
	return s.Folders.AgentFolder(r.Context(), id)
}

func (s *Server) projectContextFolder(r *http.Request) (folders.Folder, error) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		return folders.Folder{}, err
	}
	return s.Folders.ProjectContextFolder(r.Context(), id)
}

func (s *Server) globalContextFolder(*http.Request) (folders.Folder, error) {
	return s.Folders.GlobalContextFolder(), nil
}

func (s *Server) listFolder(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		q := r.URL.Query()
		limit := 0
		if v := q.Get("limit"); v != "" {
			if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > folders.MaxPageSize {
				s.fail(w, r, apperr.Invalid("limit", "须为 1 到 2000"))
				return
			}
		}
		page, err := f.List(q.Get("path"), q.Get("cursor"), limit)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, apitypes.FromFolderPage(page))
	}
}

func (s *Server) downloadFolderFile(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		file, info, err := f.Open(r.URL.Query().Get("path"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(r.URL.Query().Get("path"))}))
		http.ServeContent(w, r, "", info.ModTime(), file)
	}
}

func (s *Server) putFolderFile(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		create := false
		if v := r.URL.Query().Get("create"); v != "" {
			if create, err = strconv.ParseBool(v); err != nil {
				s.fail(w, r, apperr.Invalid("create", "只能是 true 或 false"))
				return
			}
		}
		if r.ContentLength > s.O.Limits.MaxFolderUpload {
			s.fail(w, r, apperr.TooLarge("文件超过上传上限"))
			return
		}
		e, err := f.Write(r.URL.Query().Get("path"), r.Body, s.O.Limits.MaxFolderUpload, create)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.folderChanged(r, r.URL.Query().Get("path"))
		writeJSON(w, http.StatusOK, apitypes.FromFolderEntry(e))
	}
}

func (s *Server) mkdirFolder(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in apitypes.FolderPathInput
		if err := readJSON(w, r, jsonLimit, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		e, err := f.Mkdir(in.Path)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.folderChanged(r, in.Path)
		writeJSON(w, http.StatusCreated, apitypes.FromFolderEntry(e))
	}
}

func (s *Server) moveFolderEntry(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		var in apitypes.FolderMoveInput
		if err := readJSON(w, r, jsonLimit, &in); err != nil {
			s.fail(w, r, err)
			return
		}
		e, err := f.Move(in.From, in.To)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.folderChanged(r, in.To)
		writeJSON(w, http.StatusOK, apitypes.FromFolderEntry(e))
	}
}

func (s *Server) deleteFolderEntry(of folderOf) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f, err := of(r)
		if err == nil {
			err = f.Delete(r.URL.Query().Get("path"))
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.folderChanged(r, r.URL.Query().Get("path"))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) startGitPull(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in apitypes.FolderGitPullInput
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Folders.StartGitPull(r.Context(), id, folders.GitPullInput{ActionID: in.ActionID, URL: in.URL, Branch: in.Branch, Path: in.Path})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, apitypes.FromFolderGitPull(p))
}

func (s *Server) getGitPull(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	op, err := pathUUID(r, "operationId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Folders.GitPull(r.Context(), id, op)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromFolderGitPull(p))
}

func (s *Server) listGitPulls(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "agentId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > 100 {
			s.fail(w, r, apperr.Invalid("limit", "须为 1 到 100"))
			return
		}
	}
	pulls, err := s.Folders.GitPulls(r.Context(), id, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.FolderGitPullList{Pulls: make([]apitypes.FolderGitPull, 0, len(pulls))}
	for _, p := range pulls {
		out.Pulls = append(out.Pulls, apitypes.FromFolderGitPull(p))
	}
	writeJSON(w, http.StatusOK, out)
}
