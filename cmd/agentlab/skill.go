package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/agentlab/agentlab/internal/agentskill"
)

func runSkillCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprintln(os.Stdout, "Usage: agentlab skill <show|export> [--out DIRECTORY] [--json]")
		return errHelp
	}
	command := args[0]
	if command != "show" && command != "export" {
		return unknownSubcommandError("skill", command, []string{"show", "export"})
	}
	fs := newFlagSet("skill " + command)
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	help := bindHelpFlag(fs)
	destination := ""
	if command == "export" {
		fs.StringVar(&destination, "out", "", "unused destination directory; defaults to a private versioned cache")
	}
	usage := func() {
		fmt.Fprintf(os.Stdout, "Usage: agentlab skill %s [--json]\n", command)
		if command == "export" {
			fmt.Fprintln(os.Stdout, "  --out DIRECTORY  Export the complete skill bundle to this directory")
		}
	}
	if err := parseFlags(fs, args[1:], usage, help, base.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("skill %s accepts no positional arguments", command)
	}
	if command == "show" {
		info, err := agentskill.Inspect()
		if err != nil {
			return err
		}
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(info)
		}
		fmt.Fprintf(os.Stdout, "AgentLab skill: %s\nBundle SHA-256: %s\nExport: agentlab skill export --out DIRECTORY\n", info.Manifest.Version, info.SHA256)
		return nil
	}
	result, err := agentskill.Export(ctx, destination)
	if err != nil {
		var requirement *agentskill.Error
		if errors.As(err, &requirement) {
			return &blockedRequirement{Code: requirement.Code, Requirement: "local_skill_bundle", Action: requirement.Action}
		}
		return err
	}
	if base.jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	fmt.Fprintf(os.Stdout, "AgentLab skill: %s\nSkill directory: %s\nEntrypoint: %s\n", result.Manifest.Version, result.Directory, result.Entrypoint)
	return nil
}
