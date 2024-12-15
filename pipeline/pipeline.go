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
	logger := workflow.GetLogger(ctx)
	result := &PipelineResult{Failures: []PipelineFailure{}}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
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

	// Before we build we need to verify that any generated code is current.
	fGen := workflow.ExecuteActivity(ctx, pa.GoGen, GoGenParams{
		Metadata: metadata,
		Flags:    params.TestFlags,
	})
	rGen := &GoGenResult{}
	if err := fGen.Get(ctx, rGen); err != nil {
		return nil, fmt.Errorf("GoGen activity: %w", err)
	}

	if len(rGen.ModifiedFiles) > 0 {
		result.Failures = append(result.Failures, PipelineFailure{
			Activity: "GoGen",
			Details:  rGen.ModifiedFiles,
		})
	}

	metadata = rGen.Metadata

	// Build the Go module.
	fBuild := workflow.ExecuteActivity(ctx, pa.GoBuild, GoBuildParams{
		Metadata: metadata,
		Flags:    params.TestFlags,
	})
	rBuild := &GoBuildResult{}
	if err := fBuild.Get(ctx, rBuild); err != nil {
		return nil, fmt.Errorf("GoBuild activity: %w", err)
	}

	if len(rBuild.Errors) > 0 {
		result.Failures = append(result.Failures, PipelineFailure{
			Activity: "GoBuild",
			Details:  rBuild.Errors,
		})
	}

	metadata = rBuild.Metadata

	concurActivities := []PipelineActivity{
		{"GoTest", GoTestParams{Metadata: metadata, Flags: params.TestFlags}},
		{"GoLint", GoLintParams{Metadata: metadata, Flags: params.TestFlags}},
		{"GoTidy", GoTidyParams{Metadata: metadata}},
		{"GoFmt", GoFmtParams{Metadata: metadata}},
	}
	futures := make(map[string]workflow.Future, len(concurActivities))

	// fan-out: start multiple activities concurrently
	for _, activity := range concurActivities {
		futures[activity.name] = workflow.ExecuteActivity(ctx, activity.name, activity.params)
	}

	// fan-in: await the futures and aggregate their results
	for actName, future := range futures {
		var res any

		switch actName {
		case "GoTest":
			res = &GoTestResult{}
		case "GoFmt":
			res = &GoFmtResult{}
		case "GoLint":
			res = &GoLintResult{}
		case "GoTidy":
			res = &GoTidyResult{}
		}

		if err := future.Get(ctx, res); err != nil {
			return nil, fmt.Errorf("%s activity: %w", actName, err)
		}

		switch r := res.(type) {
		case *GoTestResult:
			if len(r.FailedTests) > 0 {
				result.Failures = append(result.Failures, PipelineFailure{
					Activity: actName,
					Details:  r.FailedTests,
				})
			}
		case *GoFmtResult:
			if len(r.FailedFiles) > 0 {
				result.Failures = append(result.Failures, PipelineFailure{
					Activity: actName,
					Details:  r.FailedFiles,
				})
			}
		case *GoLintResult:
			if len(r.Issues) > 0 {
				result.Failures = append(result.Failures, PipelineFailure{
					Activity: actName,
					Details:  r.Issues,
				})
			}
		case *GoTidyResult:
			if !r.Success {
				result.Failures = append(result.Failures, PipelineFailure{
					Activity: actName,
					Details:  "Dependencies have not been updated.",
				})
			}
		}
	}

	// If checks are good, execute deploy. Otherwise, don't deploy.
	if len(result.Failures) == 0 {
		logger.Info("Pre-deployment checks have succeeded, beginning deployment.")
		// TODO: implement deployment logic
	} else {
		logger.Error("Pre-deployment checks have failed, deployment failed.", "failures", result.Failures)
	}

	// Finally, workflow finished successfully. Clean up the directory.
	fCleanup := workflow.ExecuteActivity(ctx, pa.DeleteWorkdir, DeleteWorkdirParams{
		Metadata: metadata,
	})
	if err := fCleanup.Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("DeleteWorkdir activity: %w", err)
	}

	return result, nil
}
