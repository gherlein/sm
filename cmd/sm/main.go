// Command sm mirrors a canonical skills store into agent skill directories.
package main

import (
	"os"

	"github.com/gherlein/sm/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
