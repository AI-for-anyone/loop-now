package clitemplate

import "os/exec"

// ResolveGooglePython 返回运行 Google SDK 的 Python 解释器路径。
// 优先使用 python3.13（gcloud 568 的验证辅助按 3.13.7 审阅固定），找不到时退回 python3
// （仅用于 --version 这类不加载 SDK 依赖的检查），都没有则返回空字符串。
func ResolveGooglePython() string {
	for _, name := range []string{"python3.13", "python3"} {
		if p, e := exec.LookPath(name); e == nil {
			return p
		}
	}
	return ""
}
