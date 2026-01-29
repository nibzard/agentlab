package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/agentlab/agentlab/internal/buildinfo"
)

const usageText = `agentlab is the CLI for agentlabd.

Usage:
  agentlab --version
`

func main() {
	flag.Usage = func() {
		_, _ = fmt.Fprint(os.Stdout, usageText)
	}

	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(buildinfo.String())
		return
	}

	flag.Usage()
}
