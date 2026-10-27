// Package cli 是 loopctl 的命令注册表、解析器、schema 与执行流程。
// 命令定义是唯一的事实来源：解析、schema、--help 与内置 Skill 都由它生成。
package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Identity 是调用身份。
type Identity string

// 两种身份。
const (
	IdentityAgent    Identity = "agent"
	IdentityExternal Identity = "external"
)

// FlagType 是选项的类型。
type FlagType string

// 选项类型。
const (
	TypeString      FlagType = "string"
	TypeInt         FlagType = "int"
	TypeBool        FlagType = "bool"
	TypeDuration    FlagType = "duration"
	TypeTime        FlagType = "time" // RFC 3339，例如 2026-10-13T00:00:00+08:00
	TypeEnum        FlagType = "enum"
	TypePath        FlagType = "path"
	TypePathOrStdin FlagType = "path-or-stdin"
)

// Arg 是位置参数。位置参数都是必填的；Variadic 只能用于最后一个参数，表示零到多个。
type Arg struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Variadic    bool   `json:"variadic,omitempty"`
}

// Flag 是选项，名称不带前缀 --。
type Flag struct {
	Name        string   `json:"name"`
	Type        FlagType `json:"type"`
	Required    bool     `json:"required"`
	Repeatable  bool     `json:"repeatable"`
	Default     string   `json:"default,omitempty"`
	Values      []string `json:"values,omitempty"`
	Min         *int     `json:"min,omitempty"`
	Max         *int     `json:"max,omitempty"`
	Description string   `json:"description"`
}

// ConstraintKind 是选项之间的约束。
type ConstraintKind string

// 约束种类。
const (
	ExactlyOne ConstraintKind = "exactlyOne" // 必须且只能提供其中一个
	AtMostOne  ConstraintKind = "atMostOne"  // 最多提供其中一个
	AtLeastOne ConstraintKind = "atLeastOne" // 至少提供其中一个
	Together   ConstraintKind = "together"   // 要么都提供，要么都不提供
)

// Constraint 约束一组选项。
type Constraint struct {
	Kind  ConstraintKind `json:"kind"`
	Flags []string       `json:"flags"`
	Hint  string         `json:"hint,omitempty"` // 约束不满足时附在说明后的提示
}

// RunFunc 执行命令，返回 data；错误为 *output.Error 时按其类别退出。
type RunFunc func(ctx context.Context, inv *Invocation) (any, error)

// Command 是一条命令的完整定义。
type Command struct {
	Path        []string
	Summary     string
	Identities  []Identity // 为空表示不需要身份（version、schema）
	Network     bool       // 是否连接服务端
	Args        []Arg
	Flags       []Flag
	Constraints []Constraint
	Destructive bool   // 删除类或覆盖类，必须带 --yes
	Stdin       bool   // 可能读取标准输入（由 - 或 --token-stdin 明确要求时）
	Retry       bool   // 请求失败时是否会自动重试
	Output      string // 输出说明：OpenAPI 结构名，或本地结构的描述
	Run         RunFunc
}

// Name 返回以空格连接的命令路径。
func (c *Command) Name() string { return strings.Join(c.Path, " ") }

// AllowedFor 判断身份能否使用该命令。
func (c *Command) AllowedFor(id Identity) bool {
	if len(c.Identities) == 0 {
		return true
	}
	for _, x := range c.Identities {
		if x == id {
			return true
		}
	}
	return false
}

// flag 按名称查找本命令声明的选项（不含全局选项）。
func (c *Command) flag(name string) (Flag, bool) {
	for _, f := range c.Flags {
		if f.Name == name {
			return f, true
		}
	}
	return Flag{}, false
}

// YesFlag 是删除类与覆盖类命令自动带上的确认选项。
var YesFlag = Flag{Name: "yes", Type: TypeBool, Description: "确认执行删除或覆盖；缺少时不发出请求"}

// HelpFlag 对所有命令都可用。
var HelpFlag = Flag{Name: "help", Type: TypeBool, Description: "输出该命令的说明（与 schema <命令路径> 相同），不执行命令"}

// ConnectionFlags 是连接服务端的命令接受的全局选项。
var ConnectionFlags = []Flag{
	{Name: "profile", Type: TypeString, Description: "外部身份：选择配置文件中的 profile；默认依次取 LOOPCTL_PROFILE、default"},
	{Name: "server", Type: TypeString, Description: "外部身份：对外端口的根地址（https://），优先于 LOOPCTL_URL 与 profile"},
	{Name: "ca-file", Type: TypePath, Description: "外部身份：PEM 格式的 CA 证书，指定后只信任其中的 CA；优先于 LOOPCTL_CA_FILE 与 profile"},
	{Name: "timeout", Type: TypeDuration, Description: "单次请求的超时，例如 30s；优先于 LOOPCTL_TIMEOUT，默认 60s"},
}

// Registry 是命令树。
type Registry struct {
	commands []*Command
	groups   map[string]string // 命令组路径 → 说明
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry { return &Registry{groups: map[string]string{}} }

// Group 声明一个命令组的说明。
func (r *Registry) Group(path, summary string) { r.groups[path] = summary }

// Add 注册命令；路径重复、命令与命令组同名、父路径既非命令也非命令组时 panic，由测试发现。
func (r *Registry) Add(c *Command) {
	name := c.Name()
	for _, x := range r.commands {
		if x.Name() == name {
			panic("重复的命令：" + name)
		}
	}
	if _, ok := r.groups[name]; ok {
		panic("命令与命令组同名：" + name)
	}
	for i := 1; i < len(c.Path); i++ {
		g := strings.Join(c.Path[:i], " ")
		if _, ok := r.groups[g]; !ok && r.Find(c.Path[:i]...) == nil {
			panic(fmt.Sprintf("命令 %s 缺少命令组 %s 的说明", name, g))
		}
	}
	if c.Destructive {
		if _, ok := c.flag(YesFlag.Name); !ok {
			c.Flags = append(c.Flags, YesFlag)
		}
	}
	r.commands = append(r.commands, c)
}

// Commands 返回全部命令，按路径排序。
func (r *Registry) Commands() []*Command {
	out := append([]*Command(nil), r.commands...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Find 按完整路径查找命令。
func (r *Registry) Find(path ...string) *Command {
	name := strings.Join(path, " ")
	for _, c := range r.commands {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// isGroup 判断路径是否是命令组。
func (r *Registry) isGroup(path []string) bool {
	_, ok := r.groups[strings.Join(path, " ")]
	return ok
}

// children 返回命令组的直接子项名称（子命令组与命令）。
func (r *Registry) children(path []string) []string {
	seen := map[string]bool{}
	for _, c := range r.commands {
		if len(c.Path) > len(path) && equalPrefix(c.Path, path) {
			seen[c.Path[len(path)]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalPrefix(p, prefix []string) bool {
	if len(p) < len(prefix) {
		return false
	}
	for i := range prefix {
		if p[i] != prefix[i] {
			return false
		}
	}
	return true
}

// effectiveFlags 返回命令实际接受的选项：自身选项、连接选项（仅连接服务端的命令）与 --help。
func effectiveFlags(c *Command) []Flag {
	out := append([]Flag(nil), c.Flags...)
	if c.Network {
		out = append(out, ConnectionFlags...)
	}
	return append(out, HelpFlag)
}
