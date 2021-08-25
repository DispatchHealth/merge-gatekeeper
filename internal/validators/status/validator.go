package status

import (
	"context"
	"errors"
	"fmt"

	"github.com/upsidr/merge-gatekeeper/internal/github"
	"github.com/upsidr/merge-gatekeeper/internal/multierror"
	"github.com/upsidr/merge-gatekeeper/internal/validators"
)

const (
	successState = "success"
	errorState   = "error"
	pendingState = "pending"
)

// NOTE: https://docs.github.com/en/rest/reference/checks
const (
	checkRunCompletedStatus = "completed"
)
const (
	checkRunNeutralConclusion = "neutral"
	checkRunSuccessConclusion = "success"
)

var (
	ErrInvalidCombinedStatusResponse = errors.New("github combined status response is invalid")
	ErrInvalidCheckRunResponse       = errors.New("github checkRun response is invalid")
)

type ghaStatus struct {
	Job   string
	State string
}

type statusValidator struct {
	repo        string
	owner       string
	ref         string
	selfJobName string
	client      github.Client
}

func CreateValidator(c github.Client, opts ...Option) (validators.Validator, error) {
	sv := &statusValidator{
		client: c,
	}
	for _, opt := range opts {
		opt(sv)
	}
	if err := sv.validateFields(); err != nil {
		return nil, err
	}
	return sv, nil
}

func (sv *statusValidator) Name() string {
	return sv.selfJobName
}

func (sv *statusValidator) validateFields() error {
	errs := make(multierror.Errors, 0, 6)

	if len(sv.repo) == 0 {
		errs = append(errs, errors.New("repository name is empty"))
	}
	if len(sv.owner) == 0 {
		errs = append(errs, errors.New("repository owner is empty"))
	}
	if len(sv.ref) == 0 {
		errs = append(errs, errors.New("reference of repository is empty"))
	}
	if len(sv.selfJobName) == 0 {
		errs = append(errs, errors.New("self job name is empty"))
	}
	if sv.client == nil {
		errs = append(errs, errors.New("github client is empty"))
	}

	if len(errs) != 0 {
		return errs
	}

	return nil
}

func (sv *statusValidator) Validate(ctx context.Context) (validators.Status, error) {
	ghaStatuses, err := sv.listGhaStatuses(ctx)
	if err != nil {
		return nil, err
	}

	st := &status{
		totalJobs:    make([]string, 0, len(ghaStatuses)),
		completeJobs: make([]string, 0, len(ghaStatuses)),
		succeeded:    true,
	}

	var successCnt int
	for _, ghaStatus := range ghaStatuses {
		// This job itself should be considered as success regardless of its status.
		if ghaStatus.Job == sv.selfJobName {
			successCnt++
			continue
		}
		st.totalJobs = append(st.totalJobs, ghaStatus.Job)

		if ghaStatus.State == successState {
			st.completeJobs = append(st.completeJobs, ghaStatus.Job)
			successCnt++
		}
	}

	if len(ghaStatuses) != successCnt {
		st.succeeded = false
		return st, nil
	}

	return st, nil
}

func (sv *statusValidator) listGhaStatuses(ctx context.Context) ([]*ghaStatus, error) {
	combined, _, err := sv.client.GetCombinedStatus(ctx, sv.owner, sv.repo, sv.ref, &github.ListOptions{})
	if err != nil {
		return nil, err
	}

	ghaStatuses := make([]*ghaStatus, 0, len(combined.Statuses))
	for _, s := range combined.Statuses {
		if s.Context == nil || s.State == nil {
			return nil, fmt.Errorf("%w context: %v, status: %v", ErrInvalidCombinedStatusResponse, s.Context, s.State)
		}
		ghaStatuses = append(ghaStatuses, &ghaStatus{
			Job:   *s.Context,
			State: *s.State,
		})
	}

	runResult, _, err := sv.client.ListCheckRunsForRef(ctx, sv.owner, sv.repo, sv.ref, &github.ListCheckRunsOptions{})
	if err != nil {
		return nil, err
	}

	for _, run := range runResult.CheckRuns {
		if run.Name == nil || run.Status == nil {
			return nil, fmt.Errorf("%w name: %v, status: %v", ErrInvalidCheckRunResponse, run.Name, run.Status)
		}
		ghaStatus := &ghaStatus{
			Job: *run.Name,
		}
		if *run.Status != checkRunCompletedStatus {
			ghaStatus.State = pendingState
			ghaStatuses = append(ghaStatuses, ghaStatus)
			continue
		}

		switch *run.Conclusion {
		case checkRunNeutralConclusion, checkRunSuccessConclusion:
			ghaStatus.State = successState
		default:
			ghaStatus.State = errorState
		}
		ghaStatuses = append(ghaStatuses, ghaStatus)
	}

	return ghaStatuses, nil
}
