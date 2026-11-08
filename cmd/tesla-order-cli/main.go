// Command tesla-order-cli shows Tesla order details and what changed since
// the last check.
package main

import (
	"os"

	"github.com/mauritzuph/tesla-order-cli/internal/cli"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	os.Exit(cli.Execute(os.Args[1:], cli.DefaultDeps(version)))
}
