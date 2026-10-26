package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/agentlab/agentlab/internal/clicontract"
)

func runCatalogHelp(args []string) error {
	args = clicontract.ExpandAlias(args)
	path := strings.Join(args, " ")
	if command, ok := clicontract.Commands[path]; ok {
		fmt.Printf("Usage: agentlab %s [flags]", path)
		for i := 0; i < command.Operands; i++ {
			fmt.Print(" <argument>")
		}
		fmt.Println()
		names := make([]string, 0, len(command.Flags))
		for name := range command.Flags {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			details := command.FlagDetails[name]
			description := details.Description
			if description == "" {
				description = command.Flags[name]
			}
			fmt.Printf("  --%-24s %s", name, description)
			if slices.Contains(command.RequiredFlags, name) {
				fmt.Print(" (required)")
			}
			if details.Default != "" {
				fmt.Printf(" (default: %s)", details.Default)
			}
			fmt.Println()
		}
		fmt.Println("Global flags: --endpoint URL --token TOKEN --socket PATH --timeout DURATION --json[=false]")
		fmt.Println("Flags may appear before or after operands. Use -- to preserve a literal command tail.")
		fmt.Printf("Example: agentlab %s --help\n", path)
		return nil
	}
	candidates := clicontract.Candidates(append(args, ""))
	children := make([]string, 0)
	for _, candidate := range candidates {
		if !strings.HasPrefix(candidate, "-") {
			children = append(children, candidate)
		}
	}
	if len(children) == 0 {
		return newUsageError(fmt.Errorf("unknown command %q", path), false)
	}
	fmt.Printf("Usage: agentlab %s <command>\n", path)
	for _, candidate := range children {
		fmt.Println(" " + candidate)
	}
	return nil
}
