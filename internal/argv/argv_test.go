package argv

import (
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{name: "empty", in: "", want: nil},
		{name: "whitespace only", in: "   \t\n  ", want: nil},
		{name: "simple", in: "sandbox list --json", want: []string{"sandbox", "list", "--json"}},
		{name: "collapsed spaces", in: "sandbox    list", want: []string{"sandbox", "list"}},
		{name: "double-quoted arg", in: `job new --task "fix the flaky test"`, want: []string{"job", "new", "--task", "fix the flaky test"}},
		{name: "single-quoted arg", in: `msg add 'hello world'`, want: []string{"msg", "add", "hello world"}},
		{name: "quoted empty arg preserved", in: `sandbox new --name ""`, want: []string{"sandbox", "new", "--name", ""}},
		{name: "backslash escape space", in: `path a\ b`, want: []string{"path", "a b"}},
		{name: "backslash escape quote", in: `say "he said \"hi\""`, want: []string{"say", `he said "hi"`}},
		{name: "single quotes preserve specials", in: `'no $expansion *glob*'`, want: []string{`no $expansion *glob*`}},
		{name: "no var expansion", in: `echo $HOME ${VAR}`, want: []string{"echo", "$HOME", "${VAR}"}},
		{name: "no command substitution", in: `echo $(whoami)`, want: []string{"echo", "$(whoami)"}},
		{name: "no glob expansion", in: `ls *.go`, want: []string{"ls", "*.go"}},
		{name: "no tilde expansion", in: `cat ~/file`, want: []string{"cat", "~/file"}},
		{name: "adjacent quoted and literal", in: `a"b"c`, want: []string{"abc"}},
		{name: "leading/trailing spaces", in: `  status  `, want: []string{"status"}},
		{name: "unterminated double quote", in: `echo "open`, wantErr: "unterminated double"},
		{name: "unterminated single quote", in: `echo 'open`, wantErr: "unterminated single"},
		{name: "trailing backslash", in: `echo foo\`, wantErr: "trailing backslash"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Tokenize(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Tokenize(%q) = %v, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Tokenize(%q) error = %q, want substring %q", tc.in, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Tokenize(%q) error = %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Tokenize(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("Tokenize(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}
