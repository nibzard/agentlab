// ABOUTME: Defaults command for persistent CLI preferences.
// ABOUTME: Stores defaults in XDG_CONFIG_HOME/agentlab/defaults.json.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultsFile = "defaults.json"

// wellKnownDefaults lists the recognised preference keys and their descriptions.
var wellKnownDefaults = map[string]string{
	"default-profile":  "Default profile for sandbox new",
	"default-image":    "Default container image for sandbox new",
	"default-backend":  "Default backend (proxmox, docker, libvirt)",
	"output-format":    "Default output format (text or json)",
	"default-timeout":  "Default request timeout (e.g. 30s, 2m)",
	"default-socket":   "Default daemon socket path",
}

func defaultsFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clientConfigDir, defaultsFile), nil
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
		return nil, fmt.Errorf("invalid defaults file: %w", err)
	}
	return m, nil
}

func saveDefaults(m map[string]string) error {
	path, err := defaultsFilePath()
	if err != nil {
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
  agentlab defaults delete <key>

Well-known keys:
  default-profile   Default profile for sandbox new
  default-image     Default container image for sandbox new
  default-backend   Default backend (proxmox, docker, libvirt)
  output-format     Default output format (text or json)
  default-timeout   Default request timeout (e.g. 30s, 2m)
  default-socket    Default daemon socket path

Examples:
  agentlab defaults write default-profile yolo-ephemeral
  agentlab defaults write output-format json
  agentlab defaults read default-profile
  agentlab defaults list
`

func runDefaultsCommand(_ []string, base commonFlags) error {
	fmt.Fprint(os.Stdout, defaultsUsage)
	return errHelp
}

func runDefaultsDispatch(args []string, base commonFlags) error {
	if len(args) == 0 || isHelpToken(args[0]) {
		fmt.Fprint(os.Stdout, defaultsUsage)
		return errHelp
	}
	switch args[0] {
	case "write":
		return runDefaultsWrite(args[1:], base)
	case "read":
		return runDefaultsRead(args[1:], base)
	case "list":
		return runDefaultsList(args[1:], base)
	case "delete":
		return runDefaultsDelete(args[1:], base)
	default:
		return unknownSubcommandError("defaults", args[0], defaultsSubcommands)
	}
}

func runDefaultsWrite(args []string, base commonFlags) error {
	if len(args) < 2 {
		return newUsageError(errors.New("usage: agentlab defaults write <key> <value>"), true)
	}
	key := args[0]
	value := args[1]

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
	if len(args) < 1 {
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
	if len(args) < 1 {
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

// getDefault reads a single default value, returning fallback if not set.
func getDefault(key, fallback string) string {
	m, err := loadDefaults()
	if err != nil {
		return fallback
	}
	if v, ok := m[key]; ok {
		return v
	}
	return fallback
}

// GetDefaultOutputFormat returns the preferred output format (json or text).
func GetDefaultOutputFormat() string {
	m, err := loadDefaults()
	if err != nil {
		return "text"
	}
	if v, ok := m["output-format"]; ok {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "json" || v == "text" {
			return v
		}
	}
	return "text"
}
