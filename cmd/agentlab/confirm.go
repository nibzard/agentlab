// ABOUTME: Confirmation gating for destructive or security-weakening operations.
// ABOUTME: Handles interactive prompts and --force requirements consistently.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

type confirmOptions struct {
	action     string
	force      bool
	jsonOutput bool
}

var (
	confirmReader io.Reader = os.Stdin
	confirmWriter io.Writer = os.Stderr
)

func requireConfirmation(opts confirmOptions) error {
	action := strings.TrimSpace(opts.action)
	if action == "" {
		action = "continue"
	}
	if opts.force {
		return nil
	}
	if opts.jsonOutput {
		return newCLIError(
			fmt.Sprintf("refusing to %s without --force in --json mode", action),
			"",
			fmt.Sprintf("re-run with --force to %s", action),
		)
	}
	if !isInteractive() {
		return newCLIError(
			fmt.Sprintf("refusing to %s without --force in non-interactive mode", action),
			"",
			fmt.Sprintf("re-run with --force to %s", action),
		)
	}
	ok, err := promptYesNo(confirmReader, confirmWriter, fmt.Sprintf("Confirm %s? Type 'yes' to continue: ", action))
	if err != nil {
		return err
	}
	if !ok {
		return newCLIError("aborted", "", fmt.Sprintf("re-run with --force to %s without prompting", action))
	}
	return nil
}

func promptYesNo(r io.Reader, w io.Writer, prompt string) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("stdin unavailable")
	}
	if w != nil && strings.TrimSpace(prompt) != "" {
		if _, err := fmt.Fprint(w, prompt); err != nil {
			return false, err
		}
	}
	reader := bufio.NewReader(r)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.TrimSpace(line)
	return strings.EqualFold(answer, "yes"), nil
}
