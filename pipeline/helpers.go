package pipeline

import (
	"context"
	"fmt"
	"os/exec"

	"go.temporal.io/sdk/log"
)

// getModifiedFiles uses `git status` to detect if there are any modified files in the passed directory.
func getModifiedFiles(ctx context.Context, workDir string, logger log.Logger) (string, error) {
	args := []string{"status", "--porcelain"}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir

	logger.Info("Running command", "command", "git", "args", args, "dir", workDir)

	var output []byte
	var err error
	if output, err = cmd.Output(); err != nil {
		logger.Error("Error running git status command", "error", err, "output", string(output))
		return "", fmt.Errorf("running git status command: %w", err)
	}
	return string(output), nil
}
