// Command ms ingests a proprietary agent's skills tree into the canonical store.
package main

import (
	"os"

	"github.com/gherlein/sm/internal/mscli"
)

func main() {
	os.Exit(mscli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
