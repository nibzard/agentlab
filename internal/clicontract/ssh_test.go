package clicontract

import (
	"reflect"
	"testing"
)

func TestSSHRemoteArgvAndOptions(t *testing.T) {
	for _, tc := range []struct {
		args, options []string
		target        string
		remote        []string
	}{
		{[]string{"--user", "example", "123", "--port", "2222", "echo", "quoted value", "--json"}, []string{"--user", "example", "--port", "2222"}, "123", []string{"echo", "quoted value", "--json"}},
		{[]string{"123", "--", "echo", "--help"}, nil, "123", []string{"echo", "--help"}},
		{[]string{"--identity", "--literal", "123"}, []string{"--identity", "--literal"}, "123", nil},
	} {
		options, target, remote, err := SplitSSHArgs(tc.args)
		if err != nil || !reflect.DeepEqual(options, tc.options) || target != tc.target || !reflect.DeepEqual(remote, tc.remote) {
			t.Fatalf("%v: %v %q %v %v", tc.args, options, target, remote, err)
		}
		if err := Validate(append([]string{"ssh"}, tc.args...)); err != nil {
			t.Errorf("valid SSH syntax: %v", err)
		}
	}
	global, rest := ExtractGlobals([]string{"--json", "ssh", "123", "echo", "--json=false", "--token", "literal"})
	if !reflect.DeepEqual(global, []string{"--json"}) || !reflect.DeepEqual(rest, []string{"ssh", "123", "echo", "--json=false", "--token", "literal"}) {
		t.Fatalf("remote global flags changed: %v %v", global, rest)
	}
	if _, help, err := HelpPath([]string{"ssh", "123", "echo", "--help"}); err != nil || help {
		t.Fatalf("remote help intercepted: %t %v", help, err)
	}
}
func TestSSHAndModifierInvalidSyntax(t *testing.T) {
	for _, args := range [][]string{
		{"ssh", "not-a-vmid"}, {"ssh", "123", "--port", "65536"}, {"ssh", "123", "--user="}, {"ssh", "123", "--bogus"}, {"ssh", "123", "--identity"},
		{"--json", "ssh", "123", "--exec", "--", "true"}, {"sandbox", "new", "+"}, {"sandbox", "new", "+   "},
	} {
		if err := Validate(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}

func TestGeneratedNumericOperands(t *testing.T) {
	for _, args := range [][]string{{"sandbox", "show", "nope"}, {"sandbox", "snapshot", "restore", "0", "checkpoint"}, {"workspace", "attach", "w", "-1"}, {"sandbox", "wait", "bad"}} {
		if err := Validate(args); err == nil {
			t.Errorf("accepted invalid target: %v", args)
		}
	}
	for name, command := range Commands {
		for index, kind := range command.OperandTypes {
			if kind != "vmid" || index < 0 || index >= command.Operands {
				t.Errorf("invalid generated operand metadata for %s: %v", name, command.OperandTypes)
			}
		}
	}
}
