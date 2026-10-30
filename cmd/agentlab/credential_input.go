package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type blockedRequirement struct{ Code, Requirement, Action string }

func (e *blockedRequirement) Error() string { return e.Action }

// readConnectionToken never prompts on redirected input. Explicit stdin input
// is bounded, and terminal input disables echo.
func readConnectionToken(stdin bool, jsonOutput bool) (string, error) {
	var data []byte
	var err error
	if stdin {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 16*1024+1))
		if err != nil {
			return "", fmt.Errorf("read credential input: %w", err)
		}
		if len(data) > 16*1024 {
			return "", fmt.Errorf("credential input exceeds 16 KiB")
		}
	} else if !jsonOutput && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Daemon credential: ")
		data, err = term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read credential input: %w", err)
		}
	} else {
		return "", &blockedRequirement{Code: "credentials_required", Requirement: "daemon_credential", Action: "provide a daemon credential through --token-stdin or AGENTLAB_TOKEN, then retry connect"}
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", &blockedRequirement{Code: "credentials_required", Requirement: "daemon_credential", Action: "credential input is empty; provide a daemon credential and retry connect"}
	}
	return token, nil
}
