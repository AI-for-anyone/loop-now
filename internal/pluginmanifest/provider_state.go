package pluginmanifest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ProviderState 声明插件注册的某个模型提供方有哪些状态随项目账号走（plugins/SPEC.md §13）：
// Paths 是扩展在 HOME 下保存登录与状态的相对路径（用 / 分隔），启动 Pi 时链接到账号的状态目录。
type ProviderState struct {
	Paths []string `json:"paths"`
}

// MaxProviderStatePaths 是一个提供方最多能声明的路径数。
const MaxProviderStatePaths = 8

var providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// CheckProviderState 校验 providerState 声明（清单的 loopNow.providerState 或市场条目的 providerState）。
// 路径须是 HOME 下的相对路径：不以 / 开头、不含反斜杠与冒号、没有空段、. 或 ..，不落在平台自己的 .loop-now 下。
func CheckProviderState(field string, decl map[string]ProviderState) error {
	if len(decl) > 16 {
		return invalid(field + " 最多声明 16 个提供方")
	}
	for provider, s := range decl {
		at := field + "." + provider
		if !providerIDPattern.MatchString(provider) {
			return invalid(at + "：提供方 id 不合法")
		}
		if len(s.Paths) == 0 || len(s.Paths) > MaxProviderStatePaths {
			return invalid(fmt.Sprintf("%s.paths 须有 1 到 %d 个路径", at, MaxProviderStatePaths))
		}
		seen := map[string]bool{}
		for _, p := range s.Paths {
			if err := checkStatePath(p); err != nil {
				return invalid(at + ".paths 中的 " + strconv.Quote(p) + " " + err.Error())
			}
			for q := range seen {
				if q == p || strings.HasPrefix(p, q+"/") || strings.HasPrefix(q, p+"/") {
					return invalid(at + ".paths 中的 " + strconv.Quote(p) + " 与 " + strconv.Quote(q) + " 重复或互相包含")
				}
			}
			seen[p] = true
		}
	}
	return nil
}

func checkStatePath(p string) error {
	if p == "" || len(p) > 200 || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\:") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return fmt.Errorf("须是 HOME 下的相对路径（用 / 分隔，不超过 200 字节）")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("不能有空段、. 或 ..")
		}
	}
	if p == ".loop-now" || strings.HasPrefix(p, ".loop-now/") {
		return fmt.Errorf("落在平台自己的 .loop-now 目录下")
	}
	return nil
}
