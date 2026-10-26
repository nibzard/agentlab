package clicontract

import (
	"slices"
	"strings"
	"testing"
)

func TestValidationContracts(t *testing.T) {
	for _, args := range [][]string{
		{"sandbox", "imaginary", "--bogus"}, {"--json", "sandbox", "show"}, {"sandbox", "show", "123", "--bogus"}, {"job", "run"}, {"sandbox", "revert", "123", "--restart", "--no-restart"},
	} {
		if err := Validate(args); err == nil {
			t.Errorf("accepted invalid argv %v", args)
		}
	}
	for _, args := range [][]string{
		{"help", "sandbox", "show"}, {"help", "sandbox"}, {"sandbox", "new"}, {"sandbox", "validate"}, {"pool", "status"}, {"sandbox", "show", "123", "--json"}, {"--token", "value", "sandbox", "show", "123"}, {"sandbox", "new", "--profile=default", "--tag", "one", "--tag", "two"}, {"ssh", "123", "--", "echo", "--json"}, {"job", "run", "--repo", "https://example.com/r", "--profile", "default", "--task", "quoted task"},
	} {
		if err := Validate(args); err != nil {
			t.Errorf("rejected valid argv %v: %v", args, err)
		}
	}
}
func TestCompletionConsumesGlobalValues(t *testing.T) {
	values := Candidates([]string{"--endpoint", "https://host", "sandbox", "snapshot", ""})
	for _, value := range []string{"save", "list", "restore"} {
		if !slices.Contains(values, value) {
			t.Errorf("missing %s in %v", value, values)
		}
	}
	values = Candidates([]string{"sandbox", "list", "--"})
	if !slices.Contains(values, "--all") {
		t.Errorf("missing all: %v", values)
	}
	values = Candidates([]string{"sandbox", "show", "123", "--"})
	if !slices.Contains(values, "--help") {
		t.Errorf("operand hid command flags: %v", values)
	}
	global, rest := ExtractGlobals([]string{"sandbox", "new", "--prompt", "--json", "--profile", "default"})
	if len(global) != 0 || strings.Join(rest, " ") != "sandbox new --prompt --json --profile default" {
		t.Fatalf("changed literal flag value: %v %v", global, rest)
	}
}

func TestHelpPathRespectsCommandValuesAndTails(t *testing.T) {
	for _, test := range []struct {
		args    []string
		path    string
		help    bool
		invalid bool
	}{
		{[]string{"sandbox", "show", "123", "--help"}, "sandbox show", true, false},
		{[]string{"sandbox", "--help"}, "sandbox", true, false},
		{[]string{"defaults", "read", "--help"}, "defaults read", true, false},
		{[]string{"show", "123", "--help"}, "sandbox show", true, false},
		{[]string{"sandbox", "show", "123", "--bogus", "--help"}, "", true, true},
		{[]string{"sandbox", "new", "--prompt", "--help", "--profile", "default"}, "", false, false},
		{[]string{"ssh", "123", "--", "echo", "--help"}, "", false, false},
	} {
		path, help, err := HelpPath(test.args)
		if help != test.help || (err != nil) != test.invalid || !test.invalid && strings.Join(path, " ") != test.path {
			t.Errorf("%v: path=%v help=%t err=%v", test.args, path, help, err)
		}
	}
}

func TestSharedOptionCombinations(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		valid bool
	}{
		{[]string{"sandbox", "new", "--profile", "p", "--no-wait", "--and-ssh"}, false},
		{[]string{"sandbox", "new", "--profile", "p", "--no-wait=0", "--and-ssh"}, true},
		{[]string{"sandbox", "revert", "123", "--restart=0", "--no-restart=TRUE"}, true},
		{[]string{"sandbox", "revert", "123", "--restart=1", "--no-restart=TRUE"}, false},
		{[]string{"sandbox", "update", "123"}, false},
		{[]string{"sandbox", "new", "--profile", "p", "--ttl", "-1"}, false},
		{[]string{"sandbox", "new", "--profile", "p", "--ttl", "30s"}, true},
		{[]string{"sandbox", "update", "123", "--memory", "large"}, false},
		{[]string{"sandbox", "update", "123", "--memory", "2GiB"}, true},
		{[]string{"sandbox", "update", "123", "--tag="}, true},
		{[]string{"sandbox", "update", "123", "--tag=", "--clear-tags"}, false},
		{[]string{"job", "run", "--repo", "r", "--profile", "p", "--task", "t", "--workspace-create", "w"}, false},
		{[]string{"job", "run", "--repo", "r", "--profile", "p", "--task", "t", "--workspace", "new:"}, false},
		{[]string{"job", "run", "--repo", "r", "--profile", "p", "--task", "t", "--stateful"}, true},
		{[]string{"sandbox", "stop", "--all=1"}, true},
	} {
		if err := Validate(tc.args); (err == nil) != tc.valid {
			t.Errorf("%v: valid=%t err=%v", tc.args, tc.valid, err)
		}
	}
}

func TestGeneratedRequiredFlagSyntax(t *testing.T) {
	for _, args := range [][]string{{"integration", "add"}, {"team", "member", "add", "--team", "dev"}, {"token", "create"}, {"secrets", "add-ssh-key", "--name", "example"}} {
		if Validate(args) == nil {
			t.Errorf("accepted missing registered requirement: %v", args)
		}
	}
	for _, args := range [][]string{{"integration", "add", "--name", "example", "--secret", "value"}, {"secrets", "add-ssh-key", "--name", "example", "--key-file", "/not-read-during-dry-run"}, {"token", "create", "--cmds", "*"}} {
		if err := Validate(args); err != nil {
			t.Errorf("valid syntax rejected: %v: %v", args, err)
		}
	}
}

func TestTokenClaimsValidateWithoutKeyIO(t *testing.T) {
	for _, args := range [][]string{
		{"token", "create", "--cmds", "*", "--ttl", "bad"},
		{"token", "create", "--cmds", "*", "--ttl", "-1s"},
		{"token", "create", "--cmds", ", ,"},
		{"token", "create", "--cmds", "*", "--scope", "workspace:one"},
		{"token", "create", "--cmds", "*", "--scope", "sandbox:0"},
		{"status", "--timeout", "0"},
		{"sandbox", "new", "--profile="},
		{"msg", "tail", "--job", "one", "--session", "two"},
	} {
		if err := Validate(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	duration, commands, scopes, err := ParseTokenOptions("0", " sandbox.read, job.read ", "sandbox:001, sandbox: 2")
	if err != nil || duration != 0 || len(commands) != 2 || len(scopes) != 2 || scopes[0] != "sandbox:1" || scopes[1] != "sandbox:2" {
		t.Fatalf("valid token syntax: %v %v %v %v", duration, commands, scopes, err)
	}
}

func TestHostSetupSyntaxWithoutHostActions(t *testing.T) {
	for _, args := range [][]string{{"bootstrap"}, {"bootstrap", "--host", "-bad"}, {"bootstrap", "--host", "host.example", "--ssh-user", "bad user"}, {"init", "--apply", "--control-port", "0"}, {"init", "--backend", "unknown"}, {"init", "--tailscale-serve", "--no-tailscale-serve"}} {
		if Validate(args) == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, args := range [][]string{{"bootstrap", "--host", "host.example"},
		{"bootstrap", "--host", "root@host.example"},
		{"bootstrap", "--host", "root@host.example", "--ssh-user="}, {"bootstrap", "--host", "[2001:db8::1]", "--ssh-user", "root", "--ssh-port", "2222"}, {"init", "--backend", "docker"}, {"init", "--tailscale-serve=false", "--no-tailscale-serve"}} {
		if err := Validate(args); err != nil {
			t.Errorf("rejected %v: %v", args, err)
		}
	}
}

func TestHelpBooleanAndLiteralValueParity(t *testing.T) {
	for _, args := range [][]string{{"---json", "status"}, {"status", "---json"}, {"--help=invalid"}, {"job", "run", "--repo", "--help"}, {"job", "run", "--help", "-h=false"}} {
		if Validate(args) == nil {
			t.Errorf("accepted invalid argv %v", args)
		}
	}
	for _, args := range [][]string{{"--help=true"}, {"--version=true"}, {"job", "run", "--help=true"},
		{"--help=false", "job", "run", "--help"}, {"job", "run", "--repo", "--help", "--profile", "fixture", "--task", "quoted task"}} {
		if err := Validate(args); err != nil {
			t.Errorf("rejected valid argv %v: %v", args, err)
		}
	}
}

func TestSpecialCommandSyntaxUsesActualContracts(t *testing.T) {
	for _, args := range [][]string{{"completion", "powershell"}, {"defaults", "write", "unknown", "value"}, {"defaults", "write", "default-timeout", "-1s"}, {"defaults", "write", "output-format", "yaml"}} {
		if Validate(args) == nil {
			t.Errorf("accepted invalid %v", args)
		}
	}
	for _, args := range [][]string{{"completion", "bash", "--help=false"}, {"defaults", "effective", "--help=false"}, {"defaults", "write", "output-format", "json"}} {
		if err := Validate(args); err != nil {
			t.Errorf("valid %v: %v", args, err)
		}
	}
}
