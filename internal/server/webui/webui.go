// Package webui 提供控制台的静态文件。发布构建用 embedweb 标签把 web 的构建产物嵌进二进制；
// 也可以用 LOOP_NOW_WEB_DIR 指定一个目录代替。服务端在控制台端口上把 API 之外的路径都交给这里。
package webui

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Embedded 返回嵌入的控制台文件；不带 embedweb 标签构建时为 nil。
func Embedded() fs.FS { return embedded() }

// Handler 按单页应用的规则提供 fsys 里的文件：
//   - assets/ 下是文件名带内容哈希的产物，长期缓存；不存在时 404，不回退，免得把 HTML 当脚本或样式返回；
//   - 其他存在的文件与 index.html 不缓存，更新后刷新即生效；
//   - 其他不存在的路径回退到 index.html，由前端路由处理。
//
// 只接受 GET 与 HEAD。fsys 为 nil 时一律 404，并说明怎样提供控制台。
func Handler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "只支持 GET 与 HEAD", http.StatusMethodNotAllowed)
			return
		}
		if fsys == nil {
			http.Error(w, "这个服务端没有内嵌控制台：用带 embedweb 标签的构建，或用 LOOP_NOW_WEB_DIR 指定控制台目录", http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		data, err := readFile(fsys, name)
		switch {
		case err == nil:
		case strings.HasPrefix(name, "assets/"):
			http.NotFound(w, r)
			return
		default:
			name = "index.html"
			if data, err = readFile(fsys, name); err != nil {
				http.Error(w, "控制台目录里没有 index.html", http.StatusNotFound)
				return
			}
		}
		if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		// 不给修改时间：嵌入的文件没有它，index.html 本来也不缓存。
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// readFile 读出 fsys 里的普通文件；目录与不合法的路径当作不存在。
func readFile(fsys fs.FS, name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrNotExist
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil {
		return nil, err
	} else if st.IsDir() {
		return nil, errors.New("是目录")
	}
	return io.ReadAll(f)
}
