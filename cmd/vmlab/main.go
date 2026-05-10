package main

import (
	"fmt"
	"os"

	"github.com/edihasaj/vmlab/internal/cli"
)

func main() {
	if err := cli.NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "vmlab:", err)
		os.Exit(1)
	}
}
