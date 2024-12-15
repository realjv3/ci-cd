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
type PipelineActivity struct {
	name   string
	params any
}

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

type GoGenParams struct {
	Metadata PipelineActivityMetadata
	Flags    []string
}

type GoGenResult struct {
	Metadata      PipelineActivityMetadata
	ModifiedFiles []string
}

// GoGen runs `go generate` in the specified directory.
func (pa *PipelineActivity) GoGen(ctx context.Context, params GoGenParams) (*GoGenResult, error) {
	logger := activity.GetLogger(ctx)
	result := &GoGenResult{
		Metadata:      params.Metadata,
		ModifiedFiles: []string{},
	}

	args := []string{"generate"}
	args = append(args, params.Flags...)
	args = append(args, "./...")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = result.Metadata.Workdir

	slog.Info("Running command", "command", "go", "args", args, "dir", result.Metadata.Workdir)

	// Running generators...
	if err := cmd.Run(); err != nil {
		logger.Error("Error running go generate command", "error", err)
		return nil, fmt.Errorf("running go generate command: %w", err)
	}

	// Uncommitted changes indicate generated code is not up to date and that deploy should fail.
	output, err := getModifiedFiles(ctx, result.Metadata.Workdir, logger)
	if err != nil {
		return nil, err
	}

	if len(output) > 0 {
		slog.Warn("There are uncommitted changes", "command", "git", "args", args)

		lines := strings.Split(output, "\n")
		for _, line := range lines {
			result.ModifiedFiles = append(result.ModifiedFiles, line)
		}
	}

	return result, nil
}

type GoBuildParams struct {
	Metadata PipelineActivityMetadata
	Flags    []string
}

type GoBuildResult struct {
	Metadata PipelineActivityMetadata
	Errors   string
}

// GoBuild runs `go build` in the specified directory.
func (pa *PipelineActivity) GoBuild(ctx context.Context, params GoBuildParams) (*GoBuildResult, error) {
	logger := activity.GetLogger(ctx)
	result := &GoBuildResult{
		Metadata: params.Metadata,
	}

	args := []string{"build"}
	args = append(args, params.Flags...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = result.Metadata.Workdir

	slog.Info("Running command", "command", "go", "args", args, "dir", result.Metadata.Workdir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("Error running go build command", "error", err, "output", string(output))
	}

	result.Errors = string(output)

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

type GoLintParams struct {
	Metadata PipelineActivityMetadata
	Flags    []string
}

type GoLintIssue struct {
	FromLinter string         `json:"FromLinter"`
	Text       string         `json:"Text"`
	SourceFile map[string]any `json:"Pos"`
}

type GoLintResult struct {
	Metadata PipelineActivityMetadata
	Issues   []GoLintIssue `json:"Issues"`
}

// GoLint runs `golangci-lint` in the specified directory.
func (pa *PipelineActivity) GoLint(ctx context.Context, params GoLintParams) (*GoLintResult, error) {
	logger := activity.GetLogger(ctx)

	result := &GoLintResult{
		Metadata: params.Metadata,
	}

	args := []string{"run", "--out-format=json"}
	if len(params.Flags) > 0 {
		args = append(args, "--build-tags", params.Flags[1])
	}
	slog.Info("Running command", "command", "golangci-lint", "args", args, "dir", result.Metadata.Workdir)

	cmd := exec.CommandContext(ctx, "golangci-lint", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = result.Metadata.Workdir

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// golangci-lint exits with a status code 1 when it detects linting issues
			if exitErr.ExitCode() == 1 {
				if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
					return nil, fmt.Errorf("unmarshalling golangci-lint linting issues: %w", err)
				}
			}
		} else {
			logger.Error("Error running golangci-lint command", "error", err, "stderr", stderr.String(), "stdout", stdout.String())
			return nil, fmt.Errorf("running golangci-lint command: %w", err)
		}
	}

	return result, nil
}

type GoTidyParams struct {
	Metadata PipelineActivityMetadata
}

type GoTidyResult struct {
	Metadata PipelineActivityMetadata
	Success  bool
}

// GoTidy runs `go mod tidy` in the specified directory.
func (pa *PipelineActivity) GoTidy(ctx context.Context, params GoTidyParams) (*GoTidyResult, error) {
	logger := activity.GetLogger(ctx)

	result := &GoTidyResult{
		Metadata: params.Metadata,
		Success:  true,
	}

	args := []string{"mod", "tidy"}
	args = append(args)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = result.Metadata.Workdir

	slog.Info("Running command", "command", "go", "args", args, "dir", result.Metadata.Workdir)

	tidyOut, err := cmd.CombinedOutput()
	if err != nil {
		logger.Error("`go mod tidy` encountered errors getting dependencies", "error", err, "output", string(tidyOut))
		return nil, fmt.Errorf("running go mod tidy command: %w", err)
	}

	// Uncommitted changes to go.mod/go.sum files indicate dependencies were not updated and that deploy should fail.
	diff, err := getModifiedFiles(ctx, result.Metadata.Workdir, logger)
	if err != nil {
		return nil, err
	}

	if len(diff) > 0 {
		lines := strings.Split(diff, "\n")
		for _, line := range lines {
			if strings.Contains(line, "go.mod") || strings.Contains(line, "go.sum") {
				slog.Warn("There are uncommitted changes to go.mod and go.sum files.", "command", "git", "args", args)
				result.Success = false
				break
			}
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
