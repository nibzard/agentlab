// ABOUTME: Defaults command for persistent CLI preferences.
// ABOUTME: Stores defaults in XDG_CONFIG_HOME/agentlab/defaults.json.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/agentlab/agentlab/internal/clicontract"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultsFile = "defaults.json"

func defaultsFilePath() (string, error) {
	dir, err := clientConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, defaultsFile), nil
}

func loadDefaults() (map[string]string, error) {
	path, err := defaultsFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid defaults file %q: %w", path, err)
	}
	return m, nil
}

func saveDefaults(m map[string]string) error {
	path, err := defaultsFilePath()
	if err != nil {
		return err
	}
	if err := requireConfigPathSafe(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

const defaultsUsage = `Usage:
  agentlab defaults write <key> <value>
  agentlab defaults read <key>
  agentlab defaults list
  agentlab defaults effective
  agentlab defaults delete <key>

Well-known keys:
  default-profile   Default profile for sandbox new
  default-image     Default container image for sandbox new
  output-format     Default output format (text or json)
  default-timeout   Default request timeout (e.g. 30s, 2m)
  default-socket    Default daemon socket path

Examples:
  agentlab defaults write default-profile yolo-ephemeral
  agentlab defaults write output-format json
  agentlab defaults read default-profile
  agentlab defaults list
`

func runDefaultsDispatch(args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprint(os.Stdout, defaultsUsage)
		return errHelp
	}
	if _, known := clicontract.Commands["defaults "+args[0]]; known {
		fs := newFlagSet("defaults " + args[0])
		opts := base
		opts.bind(fs)
		help := bindHelpFlag(fs)
		if err := parseFlags(fs, args[1:], func() { fmt.Fprint(os.Stdout, defaultsUsage) }, help, opts.jsonOutput); err != nil {
			return err
		}
		args = append([]string{args[0]}, fs.Args()...)
		base = opts
	}
	expected := map[string]int{"write": 3, "read": 2, "delete": 2, "list": 1, "effective": 1}
	if count, ok := expected[args[0]]; ok && len(args) != count {
		return newUsageError(fmt.Errorf("invalid arguments for defaults %s", args[0]), false)
	}
	switch args[0] {
	case "effective":
		return runDefaultsEffective(base)
	case "write":
		return runDefaultsWrite(args[1:], base)
	case "read":
		return runDefaultsRead(args[1:], base)
	case "list":
		return runDefaultsList(args[1:], base)
	case "delete":
		return runDefaultsDelete(args[1:], base)
	default:
		return unknownSubcommandError("defaults", args[0], defaultCommandNames())
	}
}

func runDefaultsWrite(args []string, base commonFlags) error {
	if len(args) != 2 {
		return newUsageError(errors.New("usage: agentlab defaults write <key> <value>"), true)
	}
	key := args[0]
	value := args[1]
	if err := validateDefault(key, value); err != nil {
		return newUsageError(err, false)
	}

	m, err := loadDefaults()
	if err != nil {
		return err
	}
	m[key] = value
	if err := saveDefaults(m); err != nil {
		return err
	}

	if base.jsonOutput {
		writeDefaultsJSON(os.Stdout, map[string]string{"key": key, "value": value})
	} else {
		fmt.Fprintf(os.Stdout, "set %s=%s\n", key, value)
	}
	return nil
}

func runDefaultsRead(args []string, base commonFlags) error {
	if len(args) != 1 {
		return newUsageError(errors.New("usage: agentlab defaults read <key>"), true)
	}
	key := args[0]

	m, err := loadDefaults()
	if err != nil {
		return err
	}
	value, ok := m[key]
	if !ok {
		if base.jsonOutput {
			writeDefaultsJSON(os.Stdout, map[string]string{"key": key, "value": "", "set": "false"})
		} else {
			fmt.Fprintf(os.Stdout, "%s is not set\n", key)
		}
		return nil
	}

	if base.jsonOutput {
		writeDefaultsJSON(os.Stdout, map[string]string{"key": key, "value": value, "set": "true"})
	} else {
		fmt.Fprintln(os.Stdout, value)
	}
	return nil
}

func runDefaultsList(args []string, base commonFlags) error {
	m, err := loadDefaults()
	if err != nil {
		return err
	}

	if base.jsonOutput {
		writeDefaultsJSON(os.Stdout, m)
		return nil
	}

	if len(m) == 0 {
		fmt.Fprintln(os.Stdout, "No defaults set.")
		fmt.Fprintln(os.Stdout, "Use: agentlab defaults write <key> <value>")
		return nil
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	maxKeyLen := 0
	for _, k := range keys {
		if len(k) > maxKeyLen {
			maxKeyLen = len(k)
		}
	}
	for _, k := range keys {
		fmt.Fprintf(os.Stdout, "%-*s  %s\n", maxKeyLen, k, m[k])
	}
	return nil
}

func runDefaultsDelete(args []string, base commonFlags) error {
	if len(args) != 1 {
		return newUsageError(errors.New("usage: agentlab defaults delete <key>"), true)
	}
	key := args[0]

	m, err := loadDefaults()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		if base.jsonOutput {
			writeDefaultsJSON(os.Stdout, map[string]string{"key": key, "deleted": "false"})
		} else {
			fmt.Fprintf(os.Stdout, "%s is not set\n", key)
		}
		return nil
	}
	delete(m, key)
	if err := saveDefaults(m); err != nil {
		return err
	}

	if base.jsonOutput {
		writeDefaultsJSON(os.Stdout, map[string]string{"key": key, "deleted": "true"})
	} else {
		fmt.Fprintf(os.Stdout, "deleted %s\n", key)
	}
	return nil
}

func writeDefaultsJSON(w io.Writer, v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "{\"error\": \"%s\"}\n", err.Error())
		return
	}
	w.Write(data)
	w.Write([]byte("\n"))
}

func defaultCommandNames() []string {
	var names []string
	for _, name := range clicontract.Candidates([]string{"defaults", ""}) {
		if !strings.HasPrefix(name, "-") {
			names = append(names, name)
		}
	}
	return names
}

func validateDefault(key, value string) error { return clicontract.ValidatePreference(key, value) }

func preferredValue(key string) string {
	values, err := loadDefaults()
	if err != nil {
		return ""
	} // parseGlobal reports malformed preferences before dispatch.
	return values[key]
}

// runDefaultsEffective reports non-secret values and their precedence sources.
func runDefaultsEffective(base commonFlags) error {
	saved, err := loadDefaults()
	if err != nil {
		return err
	}
	type effective struct {
		Value  string `json:"value"`
		Origin string `json:"origin"`
	}
	values := map[string]effective{}
	for key, value := range map[string]string{"default-socket": defaultSocketPath, "default-timeout": defaultRequestTimeout.String(), "output-format": "text", "default-profile": "", "default-image": ""} {
		origin := "built-in"
		if v, ok := saved[key]; ok {
			value, origin = v, "saved preference"
		}
		values[key] = effective{value, origin}
	}
	values["endpoint"] = effective{base.endpoint, "built-in"}
	if cfg, ok, err := loadClientConfig(); err != nil {
		return err
	} else if ok && cfg.Endpoint != "" {
		values["endpoint"] = effective{base.endpoint, "saved connection"}
	}
	if readEnvClientConfig().Endpoint != "" {
		values["endpoint"] = effective{base.endpoint, "environment"}
	}
	global, _ := clicontract.ExtractGlobals(os.Args[1:])
	for i := 0; i < len(global); i++ {
		name, _, inline := strings.Cut(strings.TrimLeft(global[i], "-"), "=")
		kind := clicontract.Globals[name]
		if !inline && kind != "bool" && i+1 < len(global) {
			i++
		}
		key := map[string]string{"socket": "default-socket", "timeout": "default-timeout", "json": "output-format", "endpoint": "endpoint"}[name]
		if key != "" {
			entry := values[key]
			entry.Origin = "CLI flag"
			values[key] = entry
		}
	}
	entry := values["default-socket"]
	entry.Value = base.socketPath
	values["default-socket"] = entry
	entry = values["default-timeout"]
	entry.Value = base.timeout.String()
	values["default-timeout"] = entry
	entry = values["output-format"]
	entry.Value = "text"
	if base.jsonOutput {
		entry.Value = "json"
	}
	values["output-format"] = entry
	if base.jsonOutput {
		writeDefaultsJSON(os.Stdout, values)
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(os.Stdout, "%s=%s (%s)\n", key, values[key].Value, values[key].Origin)
	}
	return nil
}
