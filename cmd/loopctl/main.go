// loopctl is the Loop Now command line entry point, shared by Agents on the platform and external Agents; each call
// writes exactly one JSON object to standard output.
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
