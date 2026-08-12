// Package argv provides a non-executing shell-like tokenizer that splits a
// command string into argv using POSIX-style quoting rules (review M9).
//
// It deliberately performs NO expansion: environment variables ($HOME), globs
// (*.go), tilde, and command substitution ($(whoami)) are all preserved
// literally. Every token is returned exactly as quoted/escaped so arguments
// such as  --task "fix the flaky test"  survive intact instead of being split
// on whitespace.
package argv

import (
	"errors"
	"strings"
)

// Tokenize splits s into argv.
//
// Supported syntax:
//   - Whitespace (space, tab, newline, CR) outside quotes separates tokens.
//   - Single quotes: every byte is literal until the closing single quote.
//   - Double quotes: bytes are literal except that backslash escapes ", \, $,
//     backtick, and newline (line continuation); a backslash before any other
//     byte is preserved literally.
//   - Backslash outside quotes: makes the next byte literal.
//
// An unterminated quote or a trailing backslash is an error. Tokenize never
// invokes a shell.
func Tokenize(s string) ([]string, error) {
	var args []string
	var buf strings.Builder
	inToken := false
	flush := func() {
		if inToken {
			args = append(args, buf.String())
			buf.Reset()
			inToken = false
		}
	}

	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++
		case c == '\'':
			inToken = true
			i++
			start := i
			for i < len(s) && s[i] != '\'' {
				i++
			}
			if i >= len(s) {
				return nil, errors.New("unterminated single-quoted string")
			}
			buf.WriteString(s[start:i])
			i++ // consume closing quote
		case c == '"':
			inToken = true
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] != '\\' {
					buf.WriteByte(s[i])
					i++
					continue
				}
				// Backslash inside double quotes escapes only ", \, $, `, and newline.
				i++
				if i >= len(s) {
					return nil, errors.New("unterminated escape in double-quoted string")
				}
				switch s[i] {
				case '"', '\\', '$', '`':
					buf.WriteByte(s[i])
				case '\n':
					// line continuation: emit nothing
				default:
					buf.WriteByte('\\')
					buf.WriteByte(s[i])
				}
				i++
			}
			if i >= len(s) {
				return nil, errors.New("unterminated double-quoted string")
			}
			i++ // consume closing quote
		case c == '\\':
			inToken = true
			i++
			if i >= len(s) {
				return nil, errors.New("trailing backslash")
			}
			buf.WriteByte(s[i])
			i++
		default:
			inToken = true
			buf.WriteByte(c)
			i++
		}
	}
	flush()
	return args, nil
}
