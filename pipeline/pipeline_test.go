package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
)

const repoUrl = "https://github.com/firehydrant-interviews/go-sample.git"

// Integration tests for PipelineWorkflow
type TestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *TestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()

	pa := PipelineActivity{}
	s.env.RegisterActivity(pa.GitClone)
	s.env.RegisterActivity(pa.GoBuild)
	s.env.RegisterActivity(pa.GoGen)
	s.env.RegisterActivity(pa.GoTest)
	s.env.RegisterActivity(pa.GoLint)
	s.env.RegisterActivity(pa.GoFmt)
	s.env.RegisterActivity(pa.GoTidy)
	s.env.RegisterActivity(pa.DeleteWorkdir)
}

func TestPipeline(t *testing.T) {
	suite.Run(t, new(TestSuite))
}

func (s *TestSuite) Test_Repo_Clone_Failure() {
	// setup
	s.env.OnActivity(pa.GitClone, mock.Anything, mock.Anything).Return(
		nil,
		errors.New("running git clone command"),
	)

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.ErrorContains(s.env.GetWorkflowError(), "running git clone command")
}

func (s *TestSuite) Test_Generated_Code_Not_Current() {
	// setup
	s.env.OnActivity(pa.GoGen, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoGenParams) (*GoGenResult, error) {
			s.NotZero(params.Metadata)
			return &GoGenResult{
				Metadata:      params.Metadata,
				ModifiedFiles: []string{"file1.go", "file2.go"},
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoGen", result.Failures[0].Activity)
	s.Len(result.Failures[0].Details, 2)
}

func (s *TestSuite) Test_Build_Failure() {
	// setup
	s.env.OnActivity(pa.GoBuild, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoBuildParams) (*GoBuildResult, error) {
			s.NotZero(params.Metadata)
			return &GoBuildResult{
				Metadata: params.Metadata,
				Errors:   "something went wrong",
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoBuild", result.Failures[0].Activity)
	s.Equal("something went wrong", result.Failures[0].Details)
}

func (s *TestSuite) Test_Incorrect_Code_Style() {
	// setup
	s.env.OnActivity(pa.GoFmt, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoFmtParams) (*GoFmtResult, error) {
			s.NotZero(params.Metadata)
			return &GoFmtResult{
				Metadata:    params.Metadata,
				FailedFiles: []string{"file1.go", "file2.go"},
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoFmt", result.Failures[0].Activity)
	s.Len(result.Failures[0].Details, 2)
}

func (s *TestSuite) Test_Automated_Tests_Failed() {
	// setup
	s.env.OnActivity(pa.GoTest, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoTestParams) (*GoTestResult, error) {
			s.NotZero(params.Metadata)
			return &GoTestResult{
				Metadata: params.Metadata,
				FailedTests: []GoTestCLIOutput{{
					Action:  "fail",
					Package: "github.com/very-nifty/repo",
					Test:    "Test_Something",
					Elapsed: 0.137,
				}},
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoTest", result.Failures[0].Activity)
	s.Len(result.Failures[0].Details, 1)
}

func (s *TestSuite) Test_Linter_Issues() {
	// setup
	s.env.OnActivity(pa.GoLint, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoLintParams) (*GoLintResult, error) {
			s.NotZero(params.Metadata)
			return &GoLintResult{
				Metadata: params.Metadata,
				Issues: []GoLintIssue{{
					FromLinter: "whitespace",
					Text:       "func `randomFunc`",
					SourceFile: map[string]any{"Column": 6, "Filename": "file3.go", "Line": 7, "Offset": 35},
				}},
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoLint", result.Failures[0].Activity)
	s.Len(result.Failures[0].Details, 1)
}

func (s *TestSuite) Test_Dependencies_Not_Up_To_Date() {
	// setup
	s.env.OnActivity(pa.GoTidy, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, params GoTidyParams) (*GoTidyResult, error) {
			s.NotZero(params.Metadata)
			return &GoTidyResult{
				Metadata: params.Metadata,
				Success:  false,
			}, nil
		})

	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})
	var result PipelineResult
	err := s.env.GetWorkflowResult(&result)
	if err != nil {
		s.Error(err)
	}

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.Len(result.Failures, 1)
	s.Equal("GoTidy", result.Failures[0].Activity)
	s.Equal("Dependencies have not been updated.", result.Failures[0].Details)
}

func (s *TestSuite) Test_Good_To_Deploy() {
	// unit under test
	s.env.ExecuteWorkflow(PipelineWorkflow, PipelineParams{GitURL: repoUrl})

	// assert
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}
