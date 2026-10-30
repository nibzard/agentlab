package executionarchive

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/agentlab/agentlab/internal/contentpolicy"
)

// RunCommand checks guest evidence locally before any artifact upload.
func RunCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("execution-check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	archive := fs.String("archive", "", "execution archive")
	secretFile := fs.String("secret-file", "", "private NUL-delimited credential file")
	var expected Expected
	fs.StringVar(&expected.JobID, "job-id", "", "job identifier")
	fs.StringVar(&expected.SourceID, "source-id", "", "uploaded source identifier")
	fs.StringVar(&expected.SourceRevision, "source-revision", "", "initial source revision")
	fs.StringVar(&expected.ManifestSHA256, "manifest-sha256", "", "result manifest SHA-256")
	fs.StringVar(&expected.Status, "status", "", "reported terminal status")
	fs.Func("test-command", "admitted test command (repeatable)", func(value string) error { expected.TestCommands = append(expected.TestCommands, value); return nil })
	help := fs.Bool("help", false, "show help")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *help {
		_, err := fmt.Fprintln(output, "Usage: execution-check --archive PATH --job-id ID --source-id ID --source-revision COMMIT --manifest-sha256 HASH --status STATUS [--secret-file PATH]")
		return err
	}
	if fs.NArg() != 0 || *archive == "" {
		return fmt.Errorf("provide the execution archive and job identity")
	}
	secrets, err := contentpolicy.ReadSecrets(*secretFile)
	if err != nil {
		return err
	}
	evidence, err := ValidateWithSecrets(ctx, *archive, expected, secrets)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(evidence)
}
