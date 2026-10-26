package main

import (
	"flag"
	"fmt"
	"github.com/agentlab/agentlab/internal/clicontract"
	"strings"
)

// interspersedArgs uses registered flag types to keep values (including values
// beginning with '-') attached to their flags. A literal -- ends interpretation.
func interspersedArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		name, _, inline := strings.Cut(name, "=")
		f := fs.Lookup(name)
		if f == nil {
			return nil, fmt.Errorf("flag provided but not defined: -%s", name)
		}
		flags = append(flags, a)
		boolean, ok := f.Value.(interface{ IsBoolFlag() bool })
		if !inline && !(ok && boolean.IsBoolFlag()) {
			if i+1 == len(args) {
				return nil, fmt.Errorf("flag needs an argument: -%s", name)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	return append(append(flags, "--"), positional...), nil
}

// positionalLimit is shared with command discovery. The few variable-arity
// commands explicitly opt out; all other registered commands accept no operands.
func positionalLimit(name string) int {
	if name == "ssh" {
		return -1
	}
	if command, ok := clicontract.Commands[name]; ok {
		return command.Operands
	}
	return 0
}
