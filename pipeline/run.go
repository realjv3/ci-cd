package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"
)

// PipelineActivity is a collection of Temporal Activities invokeable by PipelineWorkflow.
type PipelineActivity struct{}

type PipelineActivityMetadata struct {
	Workdir string
}

type GitCloneParams struct {
	Metadata PipelineActivityMetadata

	Remote string
}

type GitCloneResult struct {
	Metadata PipelineActivityMetadata
}

// GitClone clones a git repository to a directory. If not specified, it will be cloned to a temporary directory.
func (pa *PipelineActivity) GitClone(ctx context.Context, params GitCloneParams) (*GitCloneResult, error) {
	logger := activity.GetLogger(ctx)

	result := &GitCloneResult{
		Metadata: params.Metadata,
	}

	if params.Metadata.Workdir == "" {
		wfInfo := activity.GetInfo(ctx)

		tempDir, err := os.MkdirTemp(os.TempDir(), wfInfo.WorkflowExecution.ID)
		if err != nil {
			return nil, fmt.Errorf("creating temporary directory: %w", err)
		}

		result.Metadata.Workdir = tempDir
		slog.Info("No workdir specified, creating one", "workdir", result.Metadata.Workdir)
	}

	// Clone the repository to current directory, instead of creating a new folder based on the repository name.
	args := []string{"clone", params.Remote, "."}
	slog.Info("Running command", "command", "git", "args", args, "dir", result.Metadata.Workdir)

	cmd := exec.CommandContext(ctx, "git", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = result.Metadata.Workdir
	if err := cmd.Run(); err != nil {
		logger.Error("Error running git clone command", "error", err, "stderr", stderr.String(), "stdout", stdout.String())
		return nil, fmt.Errorf("running git clone command: %w", err)
	}
	logger.Info("Git clone command ran successfully", "stdout", stdout.String())

	return result, nil
}

type GoFmtParams struct {
	Metadata PipelineActivityMetadata
}

type GoFmtResult struct {
	Metadata    PipelineActivityMetadata
	FailedFiles []string
}

// GoFmt runs `go fmt` in the specified directory.
func (pa *PipelineActivity) GoFmt(ctx context.Context, params GoFmtParams) (*GoFmtResult, error) {
	logger := activity.GetLogger(ctx)
	result := &GoFmtResult{
		Metadata:    params.Metadata,
		FailedFiles: []string{},
	}

	args := []string{"fmt", "./..."}
	slog.Info("Running command", "command", "go", "args", args, "dir", result.Metadata.Workdir)

	cmd := exec.CommandContext(ctx, "go", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = result.Metadata.Workdir
	if err := cmd.Run(); err != nil {
		logger.Error("Error running go fmt command", "error", err, "stderr", stderr.String(), "stdout", stdout.String())
		return nil, fmt.Errorf("running go fmt command: %w", err)
	}

	files := bytes.Split(stdout.Bytes(), []byte{'\n'})
	for _, file := range files {
		if len(file) > 0 {
			result.FailedFiles = append(result.FailedFiles, string(file))
		}
	}

	return result, nil
}

type GoTestParams struct {
	Metadata PipelineActivityMetadata
	Flags    []string
}

type GoTestResult struct {
	Metadata    PipelineActivityMetadata
	FailedTests []GoTestCLIOutput
}

type GoTestCLIOutput struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
}

// GoTest runs `go test` in the specified directory.
func (pa *PipelineActivity) GoTest(ctx context.Context, params GoTestParams) (*GoTestResult, error) {
	logger := activity.GetLogger(ctx)
	result := &GoTestResult{
		Metadata:    params.Metadata,
		FailedTests: []GoTestCLIOutput{},
	}

	args := []string{"test", "-json"}
	args = append(args, params.Flags...)
	args = append(args, "./...")
	slog.Info("Running command", "command", "go", "args", args, "dir", result.Metadata.Workdir)

	cmd := exec.CommandContext(ctx, "go", args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = result.Metadata.Workdir
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// If the command exits with a non-zero status, assume it's failing tests.
			logger.Info("Command exited with non-zero status", "status", exitErr.ExitCode())
			// Parse the JSON output of `go test -json` to get the failed tests.
			body := []byte{'['}
			lines := strings.Split(stdout.String(), "\n")
			for i, line := range lines {
				body = append(body, []byte(line)...)
				if i < len(lines)-2 {
					body = append(body, byte(','))
				}
			}
			body = append(body, ']')
			var testOutput []GoTestCLIOutput
			if err := json.Unmarshal(body, &testOutput); err != nil {
				logger.Error("Error unmarshalling JSON output", "error", err, "body", string(body))
				return nil, fmt.Errorf("unmarshalling JSON output: %w", err)
			}
			for _, line := range testOutput {
				if line.Action == "fail" && line.Test != "" {
					result.FailedTests = append(result.FailedTests, line)
				}
			}
		} else {
			logger.Error("Error running go test command", "error", err, "stderr", stderr.String(), "stdout", stdout.String())
			return nil, fmt.Errorf("running go test command: %w", err)
		}
	}
	return result, nil
}

type DeleteWorkdirParams struct {
	Metadata PipelineActivityMetadata
}

// DeleteWorkdir deletes the directory specified in the metadata.
func (pa *PipelineActivity) DeleteWorkdir(ctx context.Context, params DeleteWorkdirParams) error {
	logger := activity.GetLogger(ctx)

	slog.Info("Deleting workdir", "workdir", params.Metadata.Workdir)
	if err := os.RemoveAll(params.Metadata.Workdir); err != nil {
		logger.Error("Error deleting workdir", "error", err)
		return fmt.Errorf("deleting workdir: %w", err)
	}
	logger.Info("Workdir deleted successfully")

	return nil
}
