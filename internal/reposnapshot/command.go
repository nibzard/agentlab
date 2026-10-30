package reposnapshot

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/agentlab/agentlab/internal/contentpolicy"
)

// RunResultCommand is the local result exporter used by the client and guest
// helper. It never resolves a server or reads a daemon credential.
func RunResultCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("repo-result", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	repo := fs.String("repo", ".", "task worktree")
	base := fs.String("base-revision", "", "initial task snapshot commit")
	destination := fs.String("out", "", "private output directory outside the worktree")
	secretFile := fs.String("secret-file", "", "private NUL-delimited credential file")
	fs.Bool("json", true, "output JSON")
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *help {
		_, err := fmt.Fprintln(output, "Usage: repo-result --repo PATH --base-revision COMMIT --out DIRECTORY [--json]")
		return err
	}
	if fs.NArg() != 0 || *base == "" || *destination == "" {
		return &Error{Code: "repository_result_input_required", Action: "provide the initial snapshot commit and an output directory outside the task worktree"}
	}
	secrets, err := contentpolicy.ReadSecrets(*secretFile)
	if err != nil {
		return err
	}
	result, err := Create(ctx, Options{Repository: *repo, OutputDir: *destination, BaseRevision: *base, Secrets: secrets})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
