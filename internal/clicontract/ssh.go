package clicontract

import (
	"fmt"
	"strings"
)

// SplitSSHArgs separates CLI options from the target and remote argv. Options
// may surround the target; -- or the first subsequent operand starts the remote
// command, whose flags must remain literal.
func SplitSSHArgs(args []string) (options []string, target string, remote []string, err error) {
	kinds := map[string]string{}
	for name, kind := range Commands["ssh"].Flags {
		kinds[name] = kind
	}
	for name, kind := range Globals {
		kinds[name] = kind
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remote = append([]string{}, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name, _, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			kind, known := kinds[name]
			if !known {
				return nil, "", nil, fmt.Errorf("unknown flag: %s", arg)
			}
			options = append(options, arg)
			if kind != "bool" && !inline {
				if i+1 >= len(args) {
					return nil, "", nil, fmt.Errorf("flag needs a value: %s", arg)
				}
				i++
				options = append(options, args[i])
			}
			continue
		}
		if target == "" {
			target = arg
			continue
		}
		remote = append([]string{}, args[i:]...)
		break
	}
	return
}

// ParseModifiers checks local modifier syntax. Resolving a modifier combination
// to an installed profile is daemon preflight.
func ParseModifiers(args []string) ([]string, error) {
	mods := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.HasPrefix(arg, "+") {
			return nil, fmt.Errorf("unexpected argument %q (modifiers must start with '+')", arg)
		}
		mod := strings.TrimSpace(strings.TrimPrefix(arg, "+"))
		if mod == "" {
			return nil, fmt.Errorf("modifier %q is empty", arg)
		}
		mods = append(mods, mod)
	}
	return mods, nil
}
