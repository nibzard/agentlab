package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
)

const userUsage = `Usage:
  agentlab user add --name <name> --key <ssh-public-key> [--role <role>]
  agentlab user list
  agentlab user show <name>
  agentlab user rm <name>
  agentlab user key add --name <name> --key <ssh-public-key>
  agentlab user key rm --name <name> --fingerprint <fingerprint>

Roles:
  admin   Full access: all sandboxes, user management, team management
  user    Access to own sandboxes only (default)

Note: The first user added is automatically assigned the admin role.

Examples:
  # Add an admin user:
  agentlab user add --name alice --key "ssh-ed25519 AAAA..." --role admin

  # Add a regular user:
  agentlab user add --name bob --key "ssh-ed25519 AAAA..."

  # List all users:
  agentlab user list

  # Remove a user:
  agentlab user rm bob
`

const teamUsage = `Usage:
  agentlab team add --name <name> [--description <text>]
  agentlab team list
  agentlab team rm <name>
  agentlab team members <name>
  agentlab team member add --team <name> --user <username> [--role <role>]
  agentlab team member rm --team <name> --user <username>
`

func printUserUsage()  { fmt.Fprint(os.Stdout, userUsage) }
func printTeamUsage()  { fmt.Fprint(os.Stdout, teamUsage) }

// --- User commands ---

func runUserCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printUserUsage()
		return errHelp
	}
	switch args[0] {
	case "add":
		return runUserAdd(ctx, args[1:], base)
	case "list":
		return runUserList(ctx, args[1:], base)
	case "show":
		return runUserShow(ctx, args[1:], base)
	case "rm":
		return runUserRm(ctx, args[1:], base)
	case "key":
		return runUserKeyCommand(ctx, args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown user subcommand %q", args[0]), true)
	}
}

func runUserAdd(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user add")
	opts := base
	opts.bind(fs)

	var (
		name string
		key  string
		role string
		help bool
	)
	fs.StringVar(&name, "name", "", "user name (unique identifier)")
	fs.StringVar(&key, "key", "", "SSH public key (e.g., 'ssh-ed25519 AAAA...')")
	fs.StringVar(&role, "role", "user", "user role: admin or user")
	fs.BoolVar(&help, "help", false, "show help")
	fs.BoolVar(&help, "h", false, "show help")

	if err := parseFlags(fs, args, printUserUsage, &help, opts.jsonOutput); err != nil {
		return err
	}

	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}
	if key == "" {
		return newUsageError(errors.New("--key is required (SSH public key)"), true)
	}

	reqBody := map[string]string{
		"name": name,
		"key":  key,
		"role": role,
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/users", reqBody)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("User %q created (role=%s, fingerprint=%s)\n", resp["name"], resp["role"], resp["fingerprint"])
	return nil
}

func runUserList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printUserUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/users", nil)
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}

	var resp struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Users) == 0 {
		fmt.Println("No users registered.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tROLE\tFINGERPRINT\tCREATED")
	for _, u := range resp.Users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			u["name"], u["role"], u["fingerprint"], u["created_at"])
	}
	return tw.Flush()
}

func runUserShow(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user show")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printUserUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return newUsageError(errors.New("user name is required"), true)
	}
	name := fs.Arg(0)

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/users/"+name, nil)
	if err != nil {
		return fmt.Errorf("show user: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	var resp map[string]any
	_ = json.Unmarshal(data, &resp)
	fmt.Printf("Name:         %s\n", resp["name"])
	fmt.Printf("Role:         %s\n", resp["role"])
	fmt.Printf("Fingerprint:  %s\n", resp["fingerprint"])
	fmt.Printf("Created:      %s\n", resp["created_at"])
	if keys, ok := resp["ssh_keys"].([]any); ok && len(keys) > 0 {
		fmt.Println("SSH Keys:")
		for _, k := range keys {
			if km, ok := k.(map[string]any); ok {
				fmt.Printf("  %s  (%s)\n", km["fingerprint"], km["added_at"])
			}
		}
	}
	return nil
}

func runUserRm(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user rm")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printUserUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return newUsageError(errors.New("user name is required"), true)
	}
	name := fs.Arg(0)

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/users/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("User %q deleted.\n", name)
	return nil
}

// --- User Key subcommands ---

func runUserKeyCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printUserUsage()
		return errHelp
	}
	switch args[0] {
	case "add":
		return runUserKeyAdd(ctx, args[1:], base)
	case "rm":
		return runUserKeyRm(ctx, args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown user key subcommand %q", args[0]), true)
	}
}

func runUserKeyAdd(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user key add")
	opts := base
	opts.bind(fs)

	var name, key string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "user name")
	fs.StringVar(&key, "key", "", "SSH public key to add")

	if err := parseFlags(fs, args, printUserUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}
	if key == "" {
		return newUsageError(errors.New("--key is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/users/"+name+"/keys", map[string]string{"key": key})
	if err != nil {
		return fmt.Errorf("add key: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("SSH key added to user %q.\n", name)
	return nil
}

func runUserKeyRm(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("user key rm")
	opts := base
	opts.bind(fs)

	var name, fingerprint string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "user name")
	fs.StringVar(&fingerprint, "fingerprint", "", "SSH key fingerprint to remove")

	if err := parseFlags(fs, args, printUserUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}
	if fingerprint == "" {
		return newUsageError(errors.New("--fingerprint is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/users/"+name+"/keys?fingerprint="+fingerprint, nil)
	if err != nil {
		return fmt.Errorf("remove key: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("SSH key removed from user %q.\n", name)
	return nil
}

// --- Team commands ---

func runTeamCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printTeamUsage()
		return errHelp
	}
	switch args[0] {
	case "add":
		return runTeamAdd(ctx, args[1:], base)
	case "list":
		return runTeamList(ctx, args[1:], base)
	case "rm":
		return runTeamRm(ctx, args[1:], base)
	case "members":
		return runTeamMembers(ctx, args[1:], base)
	case "member":
		return runTeamMemberCommand(ctx, args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown team subcommand %q", args[0]), true)
	}
}

func runTeamAdd(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team add")
	opts := base
	opts.bind(fs)

	var name, description string
	help := bindHelpFlag(fs)
	fs.StringVar(&name, "name", "", "team name")
	fs.StringVar(&description, "description", "", "team description")

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if name == "" {
		return newUsageError(errors.New("--name is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/teams", map[string]string{
		"name":        name,
		"description": description,
	})
	if err != nil {
		return fmt.Errorf("create team: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Team %q created.\n", resp["name"])
	return nil
}

func runTeamList(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team list")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/teams", nil)
	if err != nil {
		return fmt.Errorf("list teams: %w", err)
	}

	var resp struct {
		Teams []map[string]any `json:"teams"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Teams) == 0 {
		fmt.Println("No teams configured.")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tCREATED")
	for _, t := range resp.Teams {
		desc := "-"
		if d, ok := t["description"].(string); ok && d != "" {
			desc = d
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t["name"], desc, t["created_at"])
	}
	return tw.Flush()
}

func runTeamRm(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team rm")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return newUsageError(errors.New("team name is required"), true)
	}
	name := fs.Arg(0)

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/teams/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete team: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("Team %q deleted.\n", name)
	return nil
}

func runTeamMembers(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team members")
	opts := base
	opts.bind(fs)
	help := bindHelpFlag(fs)

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return newUsageError(errors.New("team name is required"), true)
	}
	name := fs.Arg(0)

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "GET", "/v1/teams/"+name+"/members", nil)
	if err != nil {
		return fmt.Errorf("list team members: %w", err)
	}

	var resp struct {
		Members []map[string]any `json:"members"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Members) == 0 {
		fmt.Printf("Team %q has no members.\n", name)
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "USER\tROLE\tJOINED")
	for _, m := range resp.Members {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", m["user_id"], m["role"], m["joined_at"])
	}
	return tw.Flush()
}

func runTeamMemberCommand(ctx context.Context, args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		printTeamUsage()
		return errHelp
	}
	switch args[0] {
	case "add":
		return runTeamMemberAdd(ctx, args[1:], base)
	case "rm":
		return runTeamMemberRm(ctx, args[1:], base)
	default:
		return newUsageError(fmt.Errorf("unknown team member subcommand %q", args[0]), true)
	}
}

func runTeamMemberAdd(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team member add")
	opts := base
	opts.bind(fs)

	var team, user, role string
	help := bindHelpFlag(fs)
	fs.StringVar(&team, "team", "", "team name")
	fs.StringVar(&user, "user", "", "username to add")
	fs.StringVar(&role, "role", "user", "role within team (admin or user)")

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if team == "" {
		return newUsageError(errors.New("--team is required"), true)
	}
	if user == "" {
		return newUsageError(errors.New("--user is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "POST", "/v1/teams/"+team+"/members", map[string]string{
		"user_id": user,
		"role":    role,
	})
	if err != nil {
		return fmt.Errorf("add team member: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("User %q added to team %q (role=%s).\n", user, team, role)
	return nil
}

func runTeamMemberRm(ctx context.Context, args []string, base commonFlags) error {
	fs := newFlagSet("team member rm")
	opts := base
	opts.bind(fs)

	var team, user string
	help := bindHelpFlag(fs)
	fs.StringVar(&team, "team", "", "team name")
	fs.StringVar(&user, "user", "", "username to remove")

	if err := parseFlags(fs, args, printTeamUsage, help, opts.jsonOutput); err != nil {
		return err
	}
	if team == "" {
		return newUsageError(errors.New("--team is required"), true)
	}
	if user == "" {
		return newUsageError(errors.New("--user is required"), true)
	}

	client, err := apiClientFromFlags(opts)
	if err != nil {
		return err
	}
	data, err := client.doJSON(ctx, "DELETE", "/v1/teams/"+team+"/members/"+user, nil)
	if err != nil {
		return fmt.Errorf("remove team member: %w", err)
	}

	if opts.jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(json.RawMessage(data))
	}

	fmt.Printf("User %q removed from team %q.\n", user, team)
	return nil
}
