package commands

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/conn"
	"loop-now/internal/loopctl/output"
)

var skillArg = cli.Arg{Name: "skillId", Description: "Skills 库中的模板 ID，取自 skill list"}

func addSkill(r *cli.Registry) {
	archiveFlags := []cli.Flag{
		{Name: "dir", Type: cli.TypePath, Description: "Skill 目录，由 loopctl 打包为 zip；目录中不能有符号链接或特殊文件"},
		{Name: "zip", Type: cli.TypePath, Description: "Skill 目录的 zip 压缩包"},
	}
	r.Add(&cli.Command{
		Path:       []string{"skill", "list"},
		Summary:    "列出 Skills 模板库",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Output:     "{skills: SkillTemplateSummary[]}",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "skills")
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"skill", "get"},
		Summary:    "查看模板的文件列表与 SKILL.md 内容",
		Identities: externalOnly,
		Network:    true,
		Retry:      true,
		Args:       []cli.Arg{skillArg},
		Output:     "SkillTemplate",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return get(ctx, inv, nil, "skills", inv.Args[0])
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"skill", "create"},
		Summary:     "新建模板：从本地目录、zip 文件，或只从一个 SKILL.md 创建；名称取自 SKILL.md 的 frontmatter",
		Identities:  externalOnly,
		Network:     true,
		Stdin:       true,
		Flags:       append(append([]cli.Flag(nil), archiveFlags...), skillMdFlag(false)),
		Constraints: []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"dir", "zip", "skill-md"}}},
		Output:      "SkillTemplate",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			if inv.Has("skill-md") {
				md, err := skillMarkdown(inv)
				if err != nil {
					return nil, err
				}
				return send(ctx, inv, http.MethodPost, map[string]string{"skillMd": md}, "skills")
			}
			return uploadArchive(ctx, inv, http.MethodPost, "skills")
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"skill", "replace"},
		Summary:     "用目录或 zip 整体替换模板内容（已复制给 Agent 的副本不受影响）",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{skillArg},
		Flags:       archiveFlags,
		Constraints: []cli.Constraint{{Kind: cli.ExactlyOne, Flags: []string{"dir", "zip"}}},
		Output:      "SkillTemplate",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return uploadArchive(ctx, inv, http.MethodPut, "skills", inv.Args[0])
		},
	})
	r.Add(&cli.Command{
		Path:       []string{"skill", "edit"},
		Summary:    "编辑模板的 SKILL.md（只改这个文件）",
		Identities: externalOnly,
		Network:    true,
		Stdin:      true,
		Args:       []cli.Arg{skillArg},
		Flags:      []cli.Flag{skillMdFlag(true)},
		Output:     "SkillTemplate",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			md, err := skillMarkdown(inv)
			if err != nil {
				return nil, err
			}
			return send(ctx, inv, http.MethodPut, map[string]string{"content": md}, "skills", inv.Args[0], "skill-md")
		},
	})
	r.Add(&cli.Command{
		Path:        []string{"skill", "delete"},
		Summary:     "删除模板（已复制给 Agent 的副本不受影响）",
		Identities:  externalOnly,
		Network:     true,
		Destructive: true,
		Args:        []cli.Arg{skillArg},
		Output:      "null",
		Run: func(ctx context.Context, inv *cli.Invocation) (any, error) {
			return send(ctx, inv, http.MethodDelete, nil, "skills", inv.Args[0])
		},
	})
}

// uploadArchive 以 multipart/form-data 上传 zip：--zip 直接读取文件，--dir 在本地打包。
func uploadArchive(ctx context.Context, inv *cli.Invocation, method string, path ...string) (any, error) {
	var archive []byte
	var err error
	if inv.Has("zip") {
		archive, err = readFile(inv.String("zip"))
	} else {
		archive, err = ZipDir(inv.String("dir"))
	}
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="archive"; filename="skill.zip"`)
	h.Set("Content-Type", "application/zip")
	part, _ := mw.CreatePart(h)
	part.Write(archive)
	mw.Close()
	return call(ctx, inv, conn.Request{Method: method, Path: path, Body: body.Bytes(), ContentType: mw.FormDataContentType()})
}

// ZipDir 把目录打包为 zip：条目按路径排序，相对路径用 / 分隔；遇到符号链接或特殊文件时拒绝。
func ZipDir(root string) ([]byte, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, output.Errorf(output.CodeInputUnreadable, "无法读取目录 %s：%v", root, cause(err))
	}
	if !info.IsDir() {
		return nil, output.Errorf(output.CodeInvalidArgument, "%s 不是目录", root)
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return output.Errorf(output.CodeInputUnreadable, "无法读取 %s：%v", p, cause(err))
		}
		if p == root {
			return nil
		}
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			return output.Errorf(output.CodeSymlinkNotSupported, "%s 是符号链接，不能上传", p)
		case d.IsDir():
			return nil
		case !t.IsRegular():
			return output.Errorf(output.CodeSymlinkNotSupported, "%s 不是普通文件，不能上传", p)
		}
		files = append(files, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range files {
		rel, _ := filepath.Rel(root, p)
		w, err := zw.CreateHeader(&zip.FileHeader{Name: filepath.ToSlash(rel), Method: zip.Deflate})
		if err != nil {
			return nil, output.Errorf(output.CodeInternalError, "打包失败：%v", err)
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, output.Errorf(output.CodeInputUnreadable, "无法读取 %s：%v", p, cause(err))
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return nil, output.Errorf(output.CodeInputUnreadable, "无法读取 %s：%v", p, err)
		}
	}
	if err := zw.Close(); err != nil {
		return nil, output.Errorf(output.CodeInternalError, "打包失败：%v", err)
	}
	return buf.Bytes(), nil
}
