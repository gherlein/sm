package main

import (
	"os"

	"github.com/brightsign-playground/sm/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
