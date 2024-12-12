package pipeline

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type PipelineParams struct {
	GitURL    string   `json:"git_url" yaml:"git_url"`
	TestFlags []string `json:"test_flags" yaml:"test_flags"`
}

func (pp *PipelineParams) Validate() error {
	if pp.GitURL == "" {
		return fmt.Errorf("GitURL is required")
	}
	return nil
}

type PipelineResult struct {
	Failures []PipelineFailure `json:"failures"`
}

type PipelineFailure struct {
	Activity string `json:"activity"`
	Details  any    `json:"details"`
}

var pa = PipelineActivity{}

func PipelineWorkflow(ctx workflow.Context, params PipelineParams) (*PipelineResult, error) {
	result := &PipelineResult{Failures: []PipelineFailure{}}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	fClone := workflow.ExecuteActivity(ctx, pa.GitClone, GitCloneParams{
		Remote: params.GitURL,
	})
	rClone := &GitCloneResult{}
	if err := fClone.Get(ctx, rClone); err != nil {
		return nil, fmt.Errorf("GitClone activity: %w", err)
	}

	metadata := rClone.Metadata

	fTest := workflow.ExecuteActivity(ctx, pa.GoTest, GoTestParams{
		Metadata: metadata,
		Flags:    params.TestFlags,
	})
	fFmt := workflow.ExecuteActivity(ctx, pa.GoFmt, GoFmtParams{Metadata: metadata})

	rTest := &GoTestResult{}
	if err := fTest.Get(ctx, rTest); err != nil {
		return nil, fmt.Errorf("GoTest activity: %w", err)
	}
	if len(rTest.FailedTests) > 0 {
		result.Failures = append(result.Failures, PipelineFailure{
			Activity: "GoTest",
			Details:  rTest.FailedTests,
		})
	}

	rFmt := &GoFmtResult{}
	if err := fFmt.Get(ctx, rFmt); err != nil {
		return nil, fmt.Errorf("GoFmt activity: %w", err)
	}
	if len(rFmt.FailedFiles) > 0 {
		result.Failures = append(result.Failures, PipelineFailure{
			Activity: "GoFmt",
			Details:  rFmt.FailedFiles,
		})
	}

	// TODO: Implement other checks, some ideas: go mod tidy, go build, go generate, golangci-lint, etc.
	// TODO: If checks are good, execute deploy. Otherwise, don't deploy.

	// Finally, workflow finished successfully. Clean up the directory.
	fCleanup := workflow.ExecuteActivity(ctx, pa.DeleteWorkdir, DeleteWorkdirParams{
		Metadata: metadata,
	})
	if err := fCleanup.Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("DeleteWorkdir activity: %w", err)
	}

	return result, nil
}
