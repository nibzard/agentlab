// ABOUTME: Version command with --json support.
// ABOUTME: Shows version, commit, date, and Go runtime info.

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"

	"github.com/agentlab/agentlab/internal/buildinfo"
)

const versionUsage = `Usage:
  agentlab version [--json]

Show version information.

Flags:
  --json   Output version as JSON with full build details
`

func runVersionCommand(args []string, base commonFlags) error {
	useJSON := base.jsonOutput
	for _, a := range args {
		if a == "--json" {
			useJSON = true
		}
		if isHelpToken(a) {
			fmt.Fprint(os.Stdout, versionUsage)
			return errHelp
		}
	}

	if useJSON {
		info := map[string]string{
			"version":    buildinfo.Version,
			"commit":     buildinfo.Commit,
			"date":       buildinfo.Date,
			"go_version": runtime.Version(),
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
		}
		data, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		os.Stdout.Write([]byte("\n"))
		return nil
	}

	fmt.Fprintf(os.Stdout, "agentlab %s\n", buildinfo.String())
	fmt.Fprintf(os.Stdout, "  go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}
