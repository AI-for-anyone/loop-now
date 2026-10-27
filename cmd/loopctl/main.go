// loopctl 是 Loop Now 的命令行入口，平台内 Agent 与外部 Agent 共用；每次调用只向标准输出写一个 JSON 对象。
package main

import (
	"context"
	"os"

	"loop-now/internal/buildinfo"
	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/commands"
)

func main() {
	os.Exit(cli.Run(context.Background(), commands.Registry(), os.Args[1:], cli.OSSystem(buildinfo.Version())))
}
