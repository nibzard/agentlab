// Package clicontract describes CLI syntax without performing operations.
package clicontract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed commands.json
var catalog []byte

type FlagDetails struct {
	Description string `json:"description"`
	Default     string `json:"default"`
}
type Command struct {
	RequiredFlags []string               `json:"required_flags,omitempty"`
	OperandTypes  map[int]string         `json:"operand_types,omitempty"`
	FlagDetails   map[string]FlagDetails `json:"flag_details,omitempty"`
	Flags         map[string]string      `json:"flags"`
	Operands      int                    `json:"operands"`
}

var Commands = func() map[string]Command {
	var commands map[string]Command
	if err := json.Unmarshal(catalog, &commands); err != nil {
		panic(err)
	}
	return commands
}()

var Globals = map[string]string{"endpoint": "string", "token": "string", "socket": "string", "timeout": "duration", "json": "bool", "allow-insecure-http": "bool"}
var aliases = map[string]string{"new": "sandbox new", "ls": "sandbox list", "rm": "sandbox destroy", "show": "sandbox show", "start": "sandbox start", "stop": "sandbox stop"}

// ExtractGlobals consumes only registered flags and their values, stopping at --.
// Local flag values are skipped as units, so a quoted "--json" remains a value.
func ExtractGlobals(args []string) (global, rest []string) {
	rest = []string{}
	kinds := map[string]string{}
	for _, command := range Commands {
		for name, kind := range command.Flags {
			kinds[name] = kind
		}
	}
	for name, kind := range Globals {
		kinds[name] = kind
	}
	sshTarget := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || a == "-" {
			if len(rest) > 0 && rest[0] == "ssh" {
				if sshTarget {
					rest = append(rest, args[i:]...)
					break
				}
				sshTarget = true
			}
			rest = append(rest, a)
			continue
		}
		kind, known := kinds[name]
		_, isGlobal := Globals[name]
		target := &rest
		if isGlobal || len(rest) == 0 {
			target = &global
		}
		*target = append(*target, a)
		if known && kind != "bool" && !inline && i+1 < len(args) {
			i++
			*target = append(*target, args[i])
		}
	}
	return
}

// Validate checks syntax only. It never contacts a daemon or reads/writes preferences.
func Validate(args []string) error {
	global, rest := ExtractGlobals(args)
	if err := validateFlags(global, Globals); err != nil {
		return err
	}
	if len(rest) == 0 {
		values := parsedFlagValues(global, Globals, "")
		if enabled(values["help"]) || enabled(values["version"]) {
			return nil
		}
		return fmt.Errorf("command is required")
	}
	if rest[0] == "help" {
		target := strings.Join(rest[1:], " ")
		if alias, ok := aliases[target]; ok {
			target = alias
		}
		if target == "" {
			return nil
		}
		for name := range Commands {
			if name == target || strings.HasPrefix(name, target+" ") {
				return nil
			}
		}
		return fmt.Errorf("unknown help command: %s", target)
	}
	if alias, ok := aliases[rest[0]]; ok {
		rest = append(strings.Fields(alias), rest[1:]...)
	}
	var command Command
	found := false
	consumed := 0
	for n := len(rest); n > 0; n-- {
		if c, ok := Commands[strings.Join(rest[:n], " ")]; ok {
			command, found, consumed = c, true, n
			break
		}
	}
	if !found {
		return fmt.Errorf("unknown command: %s", rest[0])
	}
	args = rest[consumed:]
	path := strings.Join(rest[:consumed], " ")
	if path == "ssh" {
		options, target, _, err := SplitSSHArgs(args)
		if err != nil {
			return err
		}
		args = append(options, target)
	}
	var operands []string
	var flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if strings.Join(rest[:consumed], " ") == "ssh" {
				break
			}
			operands = append(operands, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			operands = append(operands, a)
			continue
		}
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		kind, ok := command.Flags[name]
		if !ok {
			return fmt.Errorf("unknown flag: %s", a)
		}
		flags = append(flags, a)
		if kind != "bool" && !inline {
			if i+1 >= len(args) {
				return fmt.Errorf("flag needs a value: %s", a)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	if err := validateFlags(flags, command.Flags); err != nil {
		return err
	}
	values := parsedFlagValues(flags, command.Flags, path)
	for name, value := range parsedFlagValues(global, Globals, "") {
		if name != "help" || enabled(value) {
			values[name] = value
		}
	}
	if enabled(values["help"]) {
		return nil
	}

	required := map[string][]string{"job run": {"repo", "profile", "task"}, "job validate": {"repo", "profile", "task"}, "workspace create": {"name", "size"}}
	for _, name := range required[path] {
		if strings.TrimSpace(values[name]) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}

	if values["profile"] != "" && len(operands) > 0 && (path == "sandbox new" || path == "sandbox validate") {
		return fmt.Errorf("--profile cannot be combined with modifiers")
	}
	if err := ValidateOptions(path, values); err != nil {
		return err
	}
	operandCount := command.Operands
	if path == "secrets show" && len(operands) == 0 {
		operandCount = 0
	}
	if path == "sandbox stop" && enabled(values["all"]) {
		operandCount = 0
	}
	if path == "ssh" && len(operands) > 0 {
		operandCount = len(operands)
	}
	if operandCount >= 0 && len(operands) != operandCount {
		return fmt.Errorf("expected %d operands, got %d", command.Operands, len(operands))
	}
	if path == "sandbox new" || path == "sandbox validate" {
		if _, err := ParseModifiers(operands); err != nil {
			return err
		}
	}
	if path == "completion" {
		switch operands[0] {
		case "bash", "zsh", "fish":
		default:
			return fmt.Errorf("unsupported completion shell %q", operands[0])
		}
	}
	if path == "defaults write" {
		if err := ValidatePreference(operands[0], operands[1]); err != nil {
			return err
		}
	}
	for index, kind := range command.OperandTypes {
		if index < len(operands) && kind == "vmid" {
			if _, err := ParseVMID(operands[index]); err != nil {
				return err
			}
		}
	}
	if path == "ssh" {
		if _, err := ParseVMID(operands[0]); err != nil {
			return err
		}
	}

	return nil
}

func validateFlags(args []string, kinds map[string]string) error {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "---") {
			return fmt.Errorf("bad flag syntax: %s", args[i])
		}

		name, value, inline := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		kind, ok := kinds[name]
		if !ok {
			if name == "version" || name == "help" || name == "h" {
				kind = "bool"
			} else {
				return fmt.Errorf("unknown flag: %s", name)
			}
		}
		if !inline {
			if kind == "bool" {
				value = "true"
			} else {
				if i+1 >= len(args) {
					return fmt.Errorf("flag needs a value: %s", name)
				}
				i++
				value = args[i]
			}
		}
		var err error
		switch kind {
		case "bool":
			_, err = strconv.ParseBool(value)
		case "int":
			_, err = strconv.Atoi(value)
		case "duration":
			_, err = time.ParseDuration(value)
		}
		if err != nil {
			return fmt.Errorf("invalid --%s: %w", name, err)
		}
	}
	return nil
}

// Candidates returns context-sensitive command and flag names without inspecting
// infrastructure or preferences. Value-taking flags consume exactly one token.
func Candidates(args []string) []string {
	prefix := ""
	if len(args) > 0 {
		prefix = args[len(args)-1]
		args = args[:len(args)-1]
	}
	kinds := map[string]string{}
	for _, c := range Commands {
		for n, k := range c.Flags {
			kinds[n] = k
		}
	}
	for n, k := range Globals {
		kinds[n] = k
	}
	var words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return nil
		}
		if strings.HasPrefix(a, "-") {
			name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if kinds[name] != "bool" && !inline {
				i++
				if i >= len(args) {
					return nil
				}
			}
			continue
		}
		words = append(words, a)
	}
	if len(words) > 0 {
		if alias, ok := aliases[words[0]]; ok {
			words = append(strings.Fields(alias), words[1:]...)
		}
	}
	candidates := map[string]bool{}
	path := strings.Join(words, " ")
	for name, c := range Commands {
		parts := strings.Fields(name)
		if len(parts) > len(words) && strings.Join(parts[:len(words)], " ") == path {
			candidates[parts[len(words)]] = true
		}
		if len(words) >= len(parts) && strings.Join(words[:len(parts)], " ") == name {
			for flag := range c.Flags {
				candidates["--"+flag] = true
			}
		}
	}
	if len(words) == 0 {
		candidates["help"] = true
		candidates["--help"] = true
		candidates["--version"] = true
		for alias := range aliases {
			candidates[alias] = true
		}
	}
	for flag := range Globals {
		candidates["--"+flag] = true
	}
	var result []string
	for candidate := range candidates {
		if strings.HasPrefix(candidate, prefix) {
			result = append(result, candidate)
		}
	}
	sort.Strings(result)
	return result
}

// HelpPath identifies a command help request while respecting value-taking
// flags and literal tails. Leaf help uses the same syntax validator as dry-run.
func HelpPath(args []string) ([]string, bool, error) {
	if len(args) > 0 && args[0] == "ssh" {
		options, target, _, err := SplitSSHArgs(args[1:])
		if err != nil {
			return nil, false, err
		}
		args = append(append([]string{"ssh"}, options...), target)
	}

	kinds := map[string]string{"help": "bool", "h": "bool"}
	for _, command := range Commands {
		for name, kind := range command.Flags {
			kinds[name] = kind
		}
	}
	var words []string
	requested := false
	var seenFlags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			name, value, inline := strings.Cut(strings.TrimLeft(arg, "-"), "=")
			seenFlags = append(seenFlags, name)
			if name == "help" || name == "h" {
				if !inline {
					value = "true"
				}
				enabled, err := strconv.ParseBool(value)
				if err != nil {
					return nil, true, err
				}
				requested = enabled
			}
			if kind, known := kinds[name]; known && kind != "bool" && !inline && i+1 < len(args) {
				i++
			}
			continue
		}
		words = append(words, arg)
	}
	if !requested {
		return nil, false, nil
	}
	if len(words) > 0 {
		if alias, ok := aliases[words[0]]; ok {
			words = append(strings.Fields(alias), words[1:]...)
		}
	}
	for n := len(words); n > 0; n-- {
		if _, ok := Commands[strings.Join(words[:n], " ")]; ok {
			if err := Validate(args); err != nil {
				return nil, true, err
			}
			return words[:n], true, nil
		}
	}
	path := strings.Join(words, " ")
	for command := range Commands {
		if strings.HasPrefix(command, path+" ") {
			for _, name := range seenFlags {
				if name != "help" && name != "h" {
					return nil, true, fmt.Errorf("unknown flag: %s", name)
				}
			}
			return words, true, nil
		}
	}
	return nil, true, fmt.Errorf("unknown command: %s", path)
}

// ExpandAlias returns a fresh canonical command path for command discovery.
func ExpandAlias(args []string) []string {
	if len(args) > 0 {
		if path, ok := aliases[args[0]]; ok {
			return append(strings.Fields(path), args[1:]...)
		}
	}
	return append([]string{}, args...)
}

// ValidateOptions checks explicit option combinations shared by execution and
// syntax dry-run. It does not require saved defaults or resolve remote resources.
func ValidateOptions(path string, values map[string]string) error {
	if path == "sandbox new" || path == "sandbox validate" {
		if profile, explicit := values["profile"]; explicit && strings.TrimSpace(profile) == "" {
			return fmt.Errorf("explicit --profile cannot be empty")
		}
	}

	if value, ok := values["timeout"]; ok {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return err
		}
		if err := ValidateTimeout(timeout); err != nil {
			return err
		}
	}

	for _, name := range Commands[path].RequiredFlags {
		if strings.TrimSpace(values[name]) == "" {
			return fmt.Errorf("--%s is required", name)
		}
	}
	if path == "secrets add-ssh-key" {
		key, keyFile := strings.TrimSpace(values["key"]), strings.TrimSpace(values["key-file"])
		if key != "" && keyFile != "" {
			return fmt.Errorf("--key and --key-file are incompatible")
		}
		if key == "" && keyFile == "" {
			return fmt.Errorf("one of --key or --key-file is required")
		}
	}

	if path == "token create" {
		ttl, ok := values["ttl"]
		if !ok {
			ttl = "1h"
		}
		if _, _, _, err := ParseTokenOptions(ttl, values["cmds"], values["scope"]); err != nil {
			return err
		}
	}
	if path == "msg post" || path == "msg tail" {
		count := 0
		for _, name := range []string{"job", "workspace", "session"} {
			if strings.TrimSpace(values[name]) != "" {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("exactly one of --job, --workspace or --session is required")
		}
	}
	if path == "bootstrap" || path == "init" {
		if enabled(values["tailscale-serve"]) && enabled(values["no-tailscale-serve"]) {
			return fmt.Errorf("--tailscale-serve and --no-tailscale-serve are incompatible")
		}
		for _, name := range []string{"ssh-port", "control-port"} {
			if value, exists := values[name]; exists {
				port, err := strconv.Atoi(value)
				if err != nil || port < 1 || port > 65535 {
					return fmt.Errorf("--%s must be between 1 and 65535", name)
				}
			}
		}
	}
	if path == "bootstrap" {
		user, host := SplitUserHost(values["host"])
		if err := ValidateSSHHost(host); err != nil {
			return err
		}
		explicitUser, exists := values["ssh-user"]
		if !exists {
			user = "root"
		} else if strings.TrimSpace(explicitUser) != "" {
			user = strings.TrimSpace(explicitUser)
		}
		if user == "" {
			user = "root"
		}
		if err := ValidateSSHUser(user); err != nil {
			return err
		}
	}

	if path == "init" {
		switch strings.ToLower(strings.TrimSpace(values["backend"])) {
		case "", "proxmox", "docker", "libvirt":
		default:
			return fmt.Errorf("--backend must be proxmox, docker or libvirt")
		}
	}
	active := func(name string) bool {
		if Commands[path].Flags[name] == "bool" || Globals[name] == "bool" {
			return enabled(values[name])
		}
		value, exists := values[name]
		if name == "cores" {
			n, _ := strconv.Atoi(value)
			return n > 0
		}
		if name == "tag" {
			return exists
		} // even an empty tag is an explicit replacement
		return strings.TrimSpace(value) != ""
	}
	pairs := [][2]string{}
	switch path {
	case "ssh":
		pairs = append(pairs, [2]string{"exec", "json"})
	case "sandbox revert":
		pairs = append(pairs, [2]string{"restart", "no-restart"})
	case "sandbox new":
		pairs = append(pairs, [2]string{"no-wait", "and-ssh"})
	case "sandbox update":
		pairs = append(pairs, [2]string{"clear-tags", "tag"})
	case "job artifacts download":
		pairs = append(pairs, [2]string{"path", "name"})
	case "job run", "job validate":
		pairs = append(pairs, [2]string{"workspace", "workspace-create"})
	}
	for _, pair := range pairs {
		if active(pair[0]) && active(pair[1]) {
			return fmt.Errorf("--%s and --%s are incompatible", pair[0], pair[1])
		}
	}
	// Share value parsers with command execution, including units and rounding.
	for name, parse := range map[string]func(string) error{
		"ttl":            func(v string) error { _, err := ParseTTLMinutes(v); return err },
		"size":           func(v string) error { _, err := ParseSizeGB(v); return err },
		"workspace-size": func(v string) error { _, err := ParseSizeGB(v); return err },
		"memory":         func(v string) error { _, err := ParseMemoryMB(v); return err },
		"workspace-wait": func(v string) error { _, err := ParseWorkspaceWaitSeconds(v); return err },
	} {
		if value, exists := values[name]; exists && (strings.HasPrefix(path, "sandbox ") || strings.HasPrefix(path, "workspace ") || strings.HasPrefix(path, "session ") || path == "job run" || path == "job validate") {
			if strings.TrimSpace(value) == "" && name != "size" {
				continue
			}
			if err := parse(value); err != nil {
				return err
			}
		}
	}
	if path == "ssh" {
		for _, name := range []string{"port", "p"} {
			if value, ok := values[name]; ok {
				port, err := strconv.Atoi(value)
				if err != nil || port <= 0 || port > 65535 {
					return fmt.Errorf("invalid port %q", value)
				}
			}
		}
		for _, name := range []string{"user", "u"} {
			if value, ok := values[name]; ok && strings.TrimSpace(value) == "" {
				return fmt.Errorf("user is required")
			}
		}
	}
	if path == "job run" || path == "job validate" {
		workspace := strings.TrimSpace(values["workspace"])
		creating := active("workspace-create") || strings.HasPrefix(workspace, "new:") || (active("stateful") && workspace == "")
		if strings.HasPrefix(workspace, "new:") && strings.TrimSpace(strings.TrimPrefix(workspace, "new:")) == "" {
			return fmt.Errorf("workspace name is required after new prefix")
		}
		if creating && !active("workspace-size") && !active("stateful") {
			return fmt.Errorf("--workspace-size is required when creating a workspace")
		}
		// Branch resolution may select a session workspace; that requires daemon preflight.
		if !creating && !active("branch") && (active("workspace-size") || active("workspace-storage")) {
			return fmt.Errorf("workspace size/storage require workspace creation")
		}
	}
	if path == "sandbox update" {
		if n, _ := strconv.Atoi(values["cores"]); n < 0 {
			return fmt.Errorf("cores must be positive")
		}
	}
	if path == "sandbox update" && !active("cores") && !active("memory") && !active("tag") && !active("clear-tags") {
		return fmt.Errorf("at least one update option is required")
	}
	return nil
}
func enabled(value string) bool { result, _ := strconv.ParseBool(value); return result }

// parsedFlagValues consumes values as units and applies aliases in argv order.
// In particular, a flag-looking string value must never trigger command help.
func parsedFlagValues(args []string, kinds map[string]string, path string) map[string]string {
	values := map[string]string{}
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		kind := kinds[name]
		if name == "help" || name == "h" || name == "version" {
			kind = "bool"
		}
		if !inline {
			if kind == "bool" {
				value = "true"
			} else if i+1 < len(args) {
				i++
				value = args[i]
			}
		}
		if name == "h" {
			name = "help"
		}
		if path == "ssh" {
			switch name {
			case "e":
				name = "exec"
			case "p":
				name = "port"
			case "u":
				name = "user"
			case "i":
				name = "identity"
			}
		}
		values[name] = value
	}
	return values
}
