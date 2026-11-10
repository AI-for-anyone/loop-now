package commands

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
	"loop-now/internal/pluginmanifest"
)

var pluginArg = cli.Arg{Name: "plugin", Description: "Plugin id, from plugin list"}

func addPlugins(r *cli.Registry) {
	r.Add(&cli.Command{
		Path:       []string{"plugin", "list"},
		Summary:    "Installed plugins: status, version, number of enabling projects and number of granted Agents",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "{plugins: Plugin[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "plugins")
		},
	})
	r.Add(&cli.Command{
		Path: []string{"plugin", "install"},
		Summary: "Install a plugin: fetch it and show its manifest first, then install. Source forms: builtin:<id> or an official plugin id; a git repository address (optionally with @ref, or written as git:<address>); " +
			"npm:<package>[@version]; a local directory (an absolute path or starting with ./, on the machine where the server runs). Unofficial sources need --force to confirm",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{{Name: "source", Description: "Plugin source; see the command description for the forms"}},
		Flags:      []cli.Flag{{Name: "force", Type: cli.TypeBool, Description: "Confirm installing from an unofficial source (git, npm, a local directory)"}},
		Output:     "Plugin",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			force := inv.Bool("force")
			src, err := pluginSource(inv.Args[0])
			if err != nil {
				return nil, err
			}
			id, err := previewForInstall(ctx, inv, map[string]any{"source": src}, force)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, map[string]any{"previewId": id, "confirm": force}, "plugins")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"plugin", "update"},
		Summary:    "Fetch the latest content from the source recorded at install time and install it as a new version; the host unloads the old version before loading the new one, and a Pi that is already running keeps the old files until it restarts. Unofficial sources need --force",
		Identities: externalOnly,
		Network:    true,
		Args:       []cli.Arg{pluginArg},
		Flags:      []cli.Flag{{Name: "force", Type: cli.TypeBool, Description: "Confirm updating a plugin from an unofficial source"}},
		Output:     "Plugin",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			force := inv.Bool("force")
			id, err := previewForInstall(ctx, inv, map[string]any{"pluginId": inv.Args[0]}, force)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPost, map[string]any{"previewId": id, "confirm": force}, "plugins", inv.Args[0], "update")
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"plugin", "remove"},
		Summary:     "Uninstall a plugin: delete its configuration, enablement, grants, accounts and install directories (a local-directory source is not deleted); the data directory is deleted only with --delete-data",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{pluginArg},
		Flags:       []cli.Flag{{Name: "delete-data", Type: cli.TypeBool, Description: "Also delete the plugin's data directory"}},
		Output:      "null",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			q := url.Values{}
			if inv.Bool("delete-data") {
				q.Set("deleteData", "true")
			}
			return call(ctx, inv, conn.Request{Method: http.MethodDelete, Path: []string{"plugins", inv.Args[0]}, Query: q})
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"plugin", "validate"},
		Summary:    "Validate a plugin directory's manifest (the pi and loopNow sections of package.json) locally; sends no request",
		Identities: externalOnly,
		Args:       []cli.Arg{{Name: "dir", Description: "Plugin directory"}},
		Output:     "PluginManifest",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			m, err := readPluginDir(inv.Args[0], inv.Sys.Version)
			if err != nil {
				return nil, err
			}
			return m.Public(), nil
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"plugin", "pack"},
		Summary:    "Validate a plugin directory and pack it as an npm-format package (.tgz, without node_modules and .git); sends no request",
		Identities: externalOnly,
		Args:       []cli.Arg{{Name: "dir", Description: "Plugin directory"}},
		Flags:      []cli.Flag{{Name: "out", Type: cli.TypeString, Description: "Output file; defaults to <id>-<version>.tgz in the current directory"}},
		Output:     "{file, id, version, sha256, size}",
		Run: func(_ context.Context, inv *cli.Invocation) (any, error) {
			dir, out := inv.Args[0], inv.String("out")
			m, err := readPluginDir(dir, inv.Sys.Version)
			if err != nil {
				return nil, err
			}
			if out == "" {
				out = m.ID + "-" + m.Version + ".tgz"
			}
			sum, size, err := packPlugin(dir, out)
			if err != nil {
				return nil, err
			}
			abs, _ := filepath.Abs(out)
			return map[string]any{"file": abs, "id": m.ID, "version": m.Version, "sha256": sum, "size": size}, nil
		},
	})
}

// previewForInstall 取得预览；非官方来源没有 --force 时说明它包含什么并拒绝，不发出安装请求。
func previewForInstall(ctx context.Context, inv *cli.Invocation, body map[string]any, force bool) (string, error) {
	data, err := send(ctx, inv, http.MethodPost, body, "plugin-previews")
	if err != nil {
		return "", err
	}
	raw, _ := data.(json.RawMessage)
	var pv struct {
		PreviewID string `json:"previewId"`
		Trusted   bool   `json:"trusted"`
		Source    struct {
			Kind     string `json:"kind"`
			Location string `json:"location"`
		} `json:"source"`
		Manifest struct {
			ID          string   `json:"id"`
			Version     string   `json:"version"`
			Events      []string `json:"events"`
			Permissions []string `json:"permissions"`
			Components  struct {
				AgentExtensions int      `json:"agentExtensions"`
				Skills          []string `json:"skills"`
				MCPServers      []string `json:"mcpServers"`
				Bin             []string `json:"bin"`
				Platform        bool     `json:"platform"`
			} `json:"components"`
		} `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &pv); err != nil || pv.PreviewID == "" {
		return "", output.Errorf(output.CodeUnexpectedResponse, "预览的响应无法解析")
	}
	if !pv.Trusted && !force {
		m := pv.Manifest
		return "", output.Errorf(output.CodeConfirmationRequired,
			"插件 %s %s 来自非官方来源（%s %s）：Agent 扩展 %d 个、Skill %d 个、MCP 服务器 %v、CLI 目录 %v、平台侧 %v；订阅 %v；权限 %v。确认后加 --force 重新执行",
			m.ID, m.Version, pv.Source.Kind, pv.Source.Location, m.Components.AgentExtensions, len(m.Components.Skills), m.Components.MCPServers,
			m.Components.Bin, m.Components.Platform, m.Events, m.Permissions)
	}
	return pv.PreviewID, nil
}

// pluginSource 把 install 的来源参数解析为 PluginSource；只写 id 时是官方插件（不存在时服务端返回 not_found）。
func pluginSource(arg string) (map[string]any, error) {
	switch {
	case strings.HasPrefix(arg, "builtin:"):
		return map[string]any{"kind": "builtin", "location": strings.TrimPrefix(arg, "builtin:")}, nil
	case strings.HasPrefix(arg, "npm:"):
		return map[string]any{"kind": "npm", "location": strings.TrimPrefix(arg, "npm:")}, nil
	case strings.HasPrefix(arg, "git:"):
		return gitSource(strings.TrimPrefix(arg, "git:")), nil
	case strings.Contains(arg, "://") || strings.HasPrefix(arg, "git@") || strings.HasSuffix(strings.SplitN(arg, "@", 2)[0], ".git"):
		return gitSource(arg), nil
	case strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || arg == ".":
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, output.Errorf(output.CodeInvalidArgument, "目录无效：%v", err)
		}
		return map[string]any{"kind": "local", "location": abs}, nil
	}
	if !pluginmanifest.ValidID(arg) {
		return nil, output.Errorf(output.CodeInvalidArgument, "无法识别来源 %q：官方插件写 id，npm 包写 npm:<包名>，git 写仓库地址，本机目录写绝对路径或 ./ 开头", arg)
	}
	return map[string]any{"kind": "builtin", "location": arg}, nil
}

// gitSource 拆出地址末尾的 @ref：只有 @ 之前已经有路径（含 /）且之后不含 / 与 : 时才是 ref，git@host:... 的 @ 不算。
func gitSource(s string) map[string]any {
	out := map[string]any{"kind": "git", "location": s}
	if i := strings.LastIndex(s, "@"); i > 0 {
		before, after := s[:i], s[i+1:]
		if strings.Contains(before, "/") && after != "" && !strings.ContainsAny(after, "/:") {
			out["location"], out["ref"] = before, after
		}
	}
	return out
}

func readPluginDir(dir, version string) (pluginmanifest.Manifest, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return pluginmanifest.Manifest{}, output.Errorf(output.CodeInputUnreadable, "插件目录 %s 不存在", dir)
	}
	m, err := pluginmanifest.ReadManifest(dir, version)
	if err != nil {
		return m, output.Errorf(output.CodeInvalidArgument, "清单不合规则：%v", err)
	}
	return m, nil
}

// packPlugin 把目录写成 npm 包格式的 .tgz（顶层目录 package/），返回 sha256 与大小。
func packPlugin(dir, out string) (string, int64, error) {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, output.Errorf(output.CodeOutputUnwritable, "无法写入 %s：%v", out, cause(err))
	}
	h := sha256.New()
	gz := gzip.NewWriter(io.MultiWriter(f, h))
	tw := tar.NewWriter(gz)
	absOut, _ := filepath.Abs(out)
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != dir && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if abs, _ := filepath.Abs(path); abs == absOut || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: "package/" + filepath.ToSlash(rel), Mode: int64(info.Mode().Perm() | 0o644), Size: info.Size(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	if err == nil {
		err = gz.Close()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out)
		return "", 0, output.Errorf(output.CodeOutputUnwritable, "打包失败：%v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		return "", 0, output.Errorf(output.CodeOutputUnwritable, "%v", err)
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}
