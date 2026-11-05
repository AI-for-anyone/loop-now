// Package execenv 提供子进程环境里依赖平台的部分：系统目录、最小 PATH，以及 Windows 上子进程必需的基础变量。
// 服务端给每个子进程构造的是从空开始的环境；Unix 上只要 HOME、PATH 这几个，Windows 上 node.exe、git.exe 与
// PowerShell 还需要 SYSTEMROOT、COMSPEC、PATHEXT 这类变量以及与 HOME 对应的 USERPROFILE／APPDATA／TEMP。
package execenv

// SystemDirs 返回执行 PATH 末尾的系统目录。
func SystemDirs() []string { return systemDirs() }

// HostCommonDirs 返回排在系统目录之前的宿主常用工具目录（调用方只加入存在的）。
func HostCommonDirs() []string { return hostCommonDirs() }

// SystemPath 返回只含系统目录的最小 PATH，供只跑系统工具的子进程使用。
func SystemPath() string { return systemPath() }

// Base 返回子进程必需的平台基础变量（形如 K=V）：Unix 上没有；Windows 上是 SYSTEMROOT 等系统变量，
// 以及按 home、tmp 设置的 USERPROFILE、HOMEDRIVE/HOMEPATH、APPDATA、LOCALAPPDATA、TEMP、TMP。
// 调用方把它放在自己的变量之前，自己的同名变量（HOME、TMPDIR、PATH）照常追加。tmp 为空时取 home。
func Base(home, tmp string) []string { return base(home, tmp) }
