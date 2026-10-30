package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/agentlab/agentlab/internal/connection"
)

func contextStore() (connection.ContextStore, error) {
	root, err := clientConfigBaseDir()
	return connection.ContextStore{Root: root}, err
}

func legacySettings(cfg clientConfig) connection.Settings {
	return connection.Settings{ServerIdentity: cfg.ServerIdentity, Endpoint: cfg.Endpoint, Token: cfg.Token, JumpHost: cfg.JumpHost, JumpUser: cfg.JumpUser, AllowInsecureHTTP: cfg.AllowInsecureHTTP}
}

// requestedContext reads only global options, before the command. flag.Parse
// performs full validation later. This pass selects the saved defaults first.
func requestedContext(args []string) string {
	name := strings.TrimSpace(os.Getenv("AGENTLAB_CONTEXT"))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "--context=") {
			name = strings.TrimPrefix(arg, "--context=")
			continue
		}
		switch arg {
		case "--context":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		case "--endpoint", "--token", "--socket", "--timeout":
			i++
		}
	}
	return name
}

func runContextCommand(args []string, base commonFlags) error {
	fs := newFlagSet("context")
	help := bindHelpFlag(fs)
	fs.BoolVar(&base.jsonOutput, "json", base.jsonOutput, jsonFlagDescription)
	credentialMode := fs.String("credential-store", "", "new credential storage: auto, os, or file")
	usage := func() {
		fmt.Fprintln(os.Stdout, "Usage: agentlab context <list|use NAME|credentials NAME --credential-store auto|os|file|credentials-pending|credentials-cleanup>")
	}
	if err := parseFlags(fs, args, usage, help, base.jsonOutput); err != nil {
		return err
	}
	args = fs.Args()
	if len(args) == 0 {
		usage()
		return errHelp
	}
	store, err := contextStore()
	if err != nil {
		return err
	}
	if *credentialMode != "" {
		if _, err := connection.CredentialStorageMode(*credentialMode); err != nil {
			return err
		}
		store.CredentialMode = *credentialMode
	}
	if args[0] != "credentials-pending" && args[0] != "credentials-cleanup" {
		path, err := clientConfigPath()
		if err != nil {
			return err
		}
		cfg, _, err := loadClientConfigFrom(path)
		if err != nil {
			return err
		}
		if err := store.Migrate(legacySettings(cfg)); err != nil {
			return err
		}
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("context list accepts no arguments")
		}
		items, active, err := store.List()
		if err != nil {
			return err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"active": active, "contexts": items})
		}
		for _, item := range items {
			marker := " "
			if item.Name == active {
				marker = "*"
			}
			storage := item.CredentialStorage
			if storage == "" {
				storage = "file"
			}
			fmt.Fprintf(os.Stdout, "%s %s\t%s\t%s\n", marker, item.Name, item.Endpoint, storage)
		}
		return nil
	case "use":
		if len(args) != 2 {
			return fmt.Errorf("context use requires one name")
		}
		if err := store.Use(args[1]); err != nil {
			return err
		}
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{"active": args[1]})
		}
		fmt.Fprintf(os.Stdout, "Selected context: %s\n", args[1])
		return nil
	case "credentials":
		if len(args) != 2 || *credentialMode == "" {
			return newUsageError(fmt.Errorf("context credentials requires one name and --credential-store auto, os, or file"), false)
		}
		cfg, _, found, err := store.Resolve(args[1])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("context is unavailable")
		}
		if err := store.Put(args[1], cfg); err != nil {
			return err
		}
		items, _, err := store.List()
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Name != args[1] {
				continue
			}
			if base.jsonOutput {
				return json.NewEncoder(os.Stdout).Encode(item)
			}
			fmt.Fprintf(os.Stdout, "Context %s credential storage: %s\n", item.Name, item.CredentialStorage)
			return nil
		}
		return fmt.Errorf("saved context is unavailable")
	case "credentials-pending":
		if len(args) != 1 {
			return newUsageError(fmt.Errorf("context credentials-pending accepts no arguments"), false)
		}
		items, err := store.PendingCredentials()
		if err != nil {
			return err
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"credential_transactions": items})
		}
		for _, item := range items {
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\n", item.ID, item.Context, item.CredentialStorage, item.CredentialRef)
		}
		if len(items) == 0 {
			fmt.Fprintln(os.Stdout, "No pending credential writes")
		}
		return nil
	case "credentials-cleanup":
		if len(args) != 1 {
			return newUsageError(fmt.Errorf("context credentials-cleanup accepts no arguments"), false)
		}
		if err := store.CleanupCredentials(); err != nil {
			return err
		}
		if base.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(map[string]bool{"credential_cleanup_complete": true})
		}
		fmt.Fprintln(os.Stdout, "Pending and retired credential cleanup complete")
		return nil
	default:
		return fmt.Errorf("unknown context command %q", args[0])
	}
}

func globalCommand(args []string) string {
	rest := globalCommandArgs(args)
	if len(rest) > 0 {
		return rest[0]
	}
	return ""
}

func globalCommandArgs(args []string) []string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return args[i:]
		}
		switch arg {
		case "--context", "--endpoint", "--token", "--socket", "--timeout":
			i++
		}
	}
	return nil
}
