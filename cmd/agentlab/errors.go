// ABOUTME: Helpers for consistent CLI error messages with hints and next steps.
// ABOUTME: Provides structured error metadata for human-friendly output.

package main

import (
	"errors"
	"io"
	"strings"
)

type printedJSONOnlyError struct {
	err error
}

func (e *printedJSONOnlyError) Error() string {
	if e == nil {
		return "validation failed"
	}
	if e.err != nil {
		return strings.TrimSpace(e.err.Error())
	}
	return "validation failed"
}

func (e *printedJSONOnlyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newPrintedJSONOnlyError(err error) error {
	if err == nil {
		return &printedJSONOnlyError{err: errors.New("validation failed")}
	}
	return &printedJSONOnlyError{err: err}
}

type cliError struct {
	msg   string
	next  string
	hints []string
	err   error
}

func (e *cliError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.msg) != "" {
		return e.msg
	}
	if e.err != nil {
		return e.err.Error()
	}
	return "unknown error"
}

func (e *cliError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newCLIError(msg, next string, hints ...string) error {
	return &cliError{
		msg:   strings.TrimSpace(msg),
		next:  strings.TrimSpace(next),
		hints: normalizeHints(hints),
	}
}

func wrapCLIError(err error, msg, next string, hints ...string) error {
	if err == nil {
		return newCLIError(msg, next, hints...)
	}
	return &cliError{
		msg:   strings.TrimSpace(msg),
		next:  strings.TrimSpace(next),
		hints: normalizeHints(hints),
		err:   err,
	}
}

func withNext(err error, next string) error {
	if err == nil {
		return nil
	}
	next = strings.TrimSpace(next)
	if next == "" {
		return err
	}
	var ce *cliError
	if errors.As(err, &ce) {
		if strings.TrimSpace(ce.next) == "" {
			ce.next = next
		}
		return err
	}
	return &cliError{err: err, next: next}
}

func withHints(err error, hints ...string) error {
	if err == nil {
		return nil
	}
	hints = normalizeHints(hints)
	if len(hints) == 0 {
		return err
	}
	var ce *cliError
	if errors.As(err, &ce) {
		ce.hints = normalizeHints(append(ce.hints, hints...))
		return err
	}
	return &cliError{err: err, hints: hints}
}

func withDefaultNext(err error, next string) error {
	if err == nil || errors.Is(err, errHelp) {
		return err
	}
	return withNext(err, next)
}

func describeError(err error) (string, string, []string) {
	if err == nil {
		return "", "", nil
	}
	var ce *cliError
	if errors.As(err, &ce) {
		msg := strings.TrimSpace(ce.msg)
		next := strings.TrimSpace(ce.next)
		hints := normalizeHints(ce.hints)
		if msg == "" {
			msg = errorMessage(err)
		}
		return msg, next, hints
	}
	return errorMessage(err), "", nil
}

func normalizeHints(hints []string) []string {
	seen := make(map[string]struct{}, len(hints))
	out := make([]string, 0, len(hints))
	for _, hint := range hints {
		value := strings.TrimSpace(hint)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func printError(w io.Writer, msg, next string, hints []string) {
	if w == nil {
		return
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "unknown error"
	}
	_, _ = io.WriteString(w, "error: "+msg+"\n")
	next = strings.TrimSpace(next)
	if next != "" {
		_, _ = io.WriteString(w, "next: "+next+"\n")
	}
	for _, hint := range normalizeHints(hints) {
		_, _ = io.WriteString(w, "hint: "+hint+"\n")
	}
}
