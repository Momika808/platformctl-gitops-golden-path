package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultGitLabURL             = "https://example.internal"
	defaultInfraProjectID        = 29 // cluster/homelab-infra
	defaultInfraRef              = "main"
	kubeletProviderJobName       = "kubelet-cred-provider-vault"
	kubeletProviderTriggerVarKey = "RUN_KUBELET_CRED_PROVIDER"
)

type gitLabClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type gitLabPipeline struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	WebURL    string `json:"web_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type gitLabPipelineVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type gitLabJob struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Stage         string `json:"stage"`
	WebURL        string `json:"web_url"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at"`
	Duration      any    `json:"duration"`
	FailureReason string `json:"failure_reason"`
}

type gitLabMergeRequest struct {
	ID                 int    `json:"id"`
	IID                int    `json:"iid"`
	State              string `json:"state"`
	WebURL             string `json:"web_url"`
	SourceBranch       string `json:"source_branch"`
	TargetBranch       string `json:"target_branch"`
	MergeStatus        string `json:"merge_status"`
	DetailedMergeState string `json:"detailed_merge_status"`
	HasConflicts       bool   `json:"has_conflicts"`
	RebaseInProgress   bool   `json:"rebase_in_progress"`
	MergeError         string `json:"merge_error"`
}

type gitLabCreatePipelineRequest struct {
	Ref       string                         `json:"ref"`
	Variables []gitLabCreatePipelineVariable `json:"variables,omitempty"`
}

type gitLabCreatePipelineVariable struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func runInfra(args []string) error {
	if len(args) == 0 {
		return errors.New("infra subcommand is required (use: infra kubelet-provider <run|status|logs>)")
	}

	switch args[0] {
	case "kubelet-provider":
		return runInfraKubeletProvider(args[1:])
	default:
		return fmt.Errorf("unknown infra subcommand: %s", args[0])
	}
}

func runInfraKubeletProvider(args []string) error {
	if len(args) == 0 {
		return errors.New("kubelet-provider command is required (run|status|logs)")
	}

	switch args[0] {
	case "run":
		return runInfraKubeletProviderRun(args[1:])
	case "status":
		return runInfraKubeletProviderStatus(args[1:])
	case "logs":
		return runInfraKubeletProviderLogs(args[1:])
	default:
		return fmt.Errorf("unknown kubelet-provider command: %s", args[0])
	}
}

func runInfraKubeletProviderRun(args []string) error {
	fsRun := flag.NewFlagSet("infra kubelet-provider run", flag.ContinueOnError)
	fsRun.SetOutput(os.Stderr)

	var (
		projectID    int
		ref          string
		gitlabURL    string
		gitlabToken  string
		wait         bool
		timeout      time.Duration
		pollInterval time.Duration
		cancelStale  bool
		reason       string
		requestID    string
		requestedBy  string
	)

	fsRun.IntVar(&projectID, "project-id", envInt("PLATFORM_INFRA_PROJECT_ID", defaultInfraProjectID), "GitLab project ID for homelab-infra")
	fsRun.StringVar(&ref, "ref", envOrDefault("PLATFORM_INFRA_REF", defaultInfraRef), "Git ref to run in homelab-infra")
	fsRun.StringVar(&gitlabURL, "gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", defaultGitLabURL), "GitLab base URL")
	fsRun.StringVar(&gitlabToken, "gitlab-token", firstNonEmptyEnv("PLATFORM_GITLAB_TOKEN", "GITLAB_TOKEN"), "GitLab API token")
	fsRun.BoolVar(&wait, "wait", true, "wait for pipeline completion")
	fsRun.DurationVar(&timeout, "timeout", 20*time.Minute, "max wait time when --wait=true")
	fsRun.DurationVar(&pollInterval, "poll-interval", 5*time.Second, "poll interval when --wait=true")
	fsRun.BoolVar(&cancelStale, "cancel-stale", false, "cancel existing running kubelet-provider pipelines before starting")
	fsRun.StringVar(&reason, "reason", "", "optional audit reason")
	fsRun.StringVar(&requestID, "request-id", "", "optional request ID")
	fsRun.StringVar(&requestedBy, "requested-by", envOrDefault("USER", ""), "optional requester identity")

	if err := fsRun.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(gitlabToken) == "" {
		return errors.New("gitlab token is required (--gitlab-token or env PLATFORM_GITLAB_TOKEN)")
	}
	if timeout <= 0 {
		return errors.New("--timeout must be > 0")
	}
	if pollInterval <= 0 {
		return errors.New("--poll-interval must be > 0")
	}
	if requestID == "" {
		requestID = time.Now().UTC().Format("20060102T150405Z")
	}

	client, err := newGitLabClient(gitlabURL, gitlabToken, 15*time.Second)
	if err != nil {
		return err
	}

	runningPipeline, err := findLatestRunningKubeletProviderPipeline(client, projectID, ref)
	if err != nil {
		return err
	}
	if runningPipeline != nil {
		if !cancelStale {
			return fmt.Errorf("existing kubelet-provider pipeline is already running: #%d %s (use --cancel-stale)", runningPipeline.ID, runningPipeline.WebURL)
		}
		if err := client.cancelPipeline(projectID, runningPipeline.ID); err != nil {
			return fmt.Errorf("cancel stale pipeline #%d: %w", runningPipeline.ID, err)
		}
		fmt.Printf("Canceled stale pipeline #%d: %s\n", runningPipeline.ID, runningPipeline.WebURL)
	}

	variables := []gitLabCreatePipelineVariable{
		{Key: kubeletProviderTriggerVarKey, Value: "true"},
		{Key: "PLATFORMCTL_REQUEST_ID", Value: requestID},
	}
	if strings.TrimSpace(reason) != "" {
		variables = append(variables, gitLabCreatePipelineVariable{Key: "PLATFORMCTL_REASON", Value: reason})
	}
	if strings.TrimSpace(requestedBy) != "" {
		variables = append(variables, gitLabCreatePipelineVariable{Key: "PLATFORMCTL_REQUESTED_BY", Value: requestedBy})
	}

	pipeline, err := client.createPipeline(projectID, gitLabCreatePipelineRequest{
		Ref:       ref,
		Variables: variables,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Started pipeline #%d\n", pipeline.ID)
	fmt.Printf("URL: %s\n", pipeline.WebURL)
	fmt.Printf("Ref: %s\n", pipeline.Ref)

	if !wait {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for pipeline #%d (%s)", pipeline.ID, pipeline.WebURL)
		}

		currentPipeline, err := client.getPipeline(projectID, pipeline.ID)
		if err != nil {
			return fmt.Errorf("get pipeline status: %w", err)
		}
		jobs, err := client.listPipelineJobs(projectID, pipeline.ID)
		if err != nil {
			return fmt.Errorf("list pipeline jobs: %w", err)
		}
		kubeletJob := findJobByName(jobs, kubeletProviderJobName)

		if kubeletJob != nil {
			fmt.Printf("Pipeline=%s Job=%s\n", currentPipeline.Status, kubeletJob.Status)
		} else {
			fmt.Printf("Pipeline=%s Job=<not-created-yet>\n", currentPipeline.Status)
		}

		if isTerminalPipelineStatus(currentPipeline.Status) {
			if currentPipeline.Status == "success" && kubeletJob != nil && kubeletJob.Status == "success" {
				fmt.Println("kubelet-provider rollout finished successfully.")
				if kubeletJob.WebURL != "" {
					fmt.Printf("Job URL: %s\n", kubeletJob.WebURL)
				}
				return nil
			}

			var traceTail string
			if kubeletJob != nil {
				trace, traceErr := client.getJobTrace(projectID, kubeletJob.ID)
				if traceErr == nil {
					traceTail = tailLines(trace, 40)
				}
			}

			var reasonParts []string
			reasonParts = append(reasonParts, fmt.Sprintf("pipeline status=%s", currentPipeline.Status))
			if kubeletJob != nil {
				reasonParts = append(reasonParts, fmt.Sprintf("job status=%s", kubeletJob.Status))
				if kubeletJob.FailureReason != "" {
					reasonParts = append(reasonParts, fmt.Sprintf("failure_reason=%s", kubeletJob.FailureReason))
				}
			}
			if traceTail != "" {
				fmt.Println("---- trace tail ----")
				fmt.Println(traceTail)
			}
			return fmt.Errorf("kubelet-provider rollout failed: %s", strings.Join(reasonParts, ", "))
		}

		time.Sleep(pollInterval)
	}
}

func runInfraKubeletProviderStatus(args []string) error {
	fsStatus := flag.NewFlagSet("infra kubelet-provider status", flag.ContinueOnError)
	fsStatus.SetOutput(os.Stderr)

	var (
		projectID   int
		ref         string
		gitlabURL   string
		gitlabToken string
		pipelineID  int
		last        bool
	)

	fsStatus.IntVar(&projectID, "project-id", envInt("PLATFORM_INFRA_PROJECT_ID", defaultInfraProjectID), "GitLab project ID for homelab-infra")
	fsStatus.StringVar(&ref, "ref", envOrDefault("PLATFORM_INFRA_REF", defaultInfraRef), "Git ref")
	fsStatus.StringVar(&gitlabURL, "gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", defaultGitLabURL), "GitLab base URL")
	fsStatus.StringVar(&gitlabToken, "gitlab-token", firstNonEmptyEnv("PLATFORM_GITLAB_TOKEN", "GITLAB_TOKEN"), "GitLab API token")
	fsStatus.IntVar(&pipelineID, "pipeline", 0, "explicit pipeline ID")
	fsStatus.BoolVar(&last, "last", false, "use latest kubelet-provider pipeline for --ref")

	if err := fsStatus.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(gitlabToken) == "" {
		return errors.New("gitlab token is required (--gitlab-token or env PLATFORM_GITLAB_TOKEN)")
	}

	if pipelineID > 0 && last {
		return errors.New("use either --pipeline or --last, not both")
	}
	if pipelineID == 0 && !last {
		return errors.New("choose one mode: --pipeline <id> or --last")
	}

	client, err := newGitLabClient(gitlabURL, gitlabToken, 15*time.Second)
	if err != nil {
		return err
	}

	if last {
		p, err := findLatestKubeletProviderPipeline(client, projectID, ref)
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("no kubelet-provider pipelines found for ref=%s", ref)
		}
		pipelineID = p.ID
	}

	pipeline, err := client.getPipeline(projectID, pipelineID)
	if err != nil {
		return err
	}
	jobs, err := client.listPipelineJobs(projectID, pipelineID)
	if err != nil {
		return err
	}
	kubeletJob := findJobByName(jobs, kubeletProviderJobName)

	fmt.Printf("Pipeline #%d\n", pipeline.ID)
	fmt.Printf("Status: %s\n", pipeline.Status)
	fmt.Printf("Ref: %s\n", pipeline.Ref)
	fmt.Printf("URL: %s\n", pipeline.WebURL)
	if kubeletJob != nil {
		fmt.Printf("Job: %s (#%d)\n", kubeletJob.Name, kubeletJob.ID)
		fmt.Printf("Job status: %s\n", kubeletJob.Status)
		if kubeletJob.FailureReason != "" {
			fmt.Printf("Job failure reason: %s\n", kubeletJob.FailureReason)
		}
		if kubeletJob.WebURL != "" {
			fmt.Printf("Job URL: %s\n", kubeletJob.WebURL)
		}
	} else {
		fmt.Println("Job: not present in pipeline")
	}
	return nil
}

func runInfraKubeletProviderLogs(args []string) error {
	fsLogs := flag.NewFlagSet("infra kubelet-provider logs", flag.ContinueOnError)
	fsLogs.SetOutput(os.Stderr)

	var (
		projectID   int
		ref         string
		gitlabURL   string
		gitlabToken string
		pipelineID  int
		jobID       int
		last        bool
		tail        int
	)

	fsLogs.IntVar(&projectID, "project-id", envInt("PLATFORM_INFRA_PROJECT_ID", defaultInfraProjectID), "GitLab project ID for homelab-infra")
	fsLogs.StringVar(&ref, "ref", envOrDefault("PLATFORM_INFRA_REF", defaultInfraRef), "Git ref")
	fsLogs.StringVar(&gitlabURL, "gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", defaultGitLabURL), "GitLab base URL")
	fsLogs.StringVar(&gitlabToken, "gitlab-token", firstNonEmptyEnv("PLATFORM_GITLAB_TOKEN", "GITLAB_TOKEN"), "GitLab API token")
	fsLogs.IntVar(&pipelineID, "pipeline", 0, "explicit pipeline ID")
	fsLogs.IntVar(&jobID, "job", 0, "explicit job ID")
	fsLogs.BoolVar(&last, "last", false, "use latest kubelet-provider pipeline for --ref")
	fsLogs.IntVar(&tail, "tail", 120, "print last N lines")

	if err := fsLogs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(gitlabToken) == "" {
		return errors.New("gitlab token is required (--gitlab-token or env PLATFORM_GITLAB_TOKEN)")
	}
	if tail <= 0 {
		return errors.New("--tail must be > 0")
	}

	client, err := newGitLabClient(gitlabURL, gitlabToken, 15*time.Second)
	if err != nil {
		return err
	}

	if jobID == 0 {
		if pipelineID > 0 && last {
			return errors.New("use either --pipeline or --last, not both")
		}
		if pipelineID == 0 {
			if !last {
				return errors.New("choose one mode: --job <id> or --pipeline <id> or --last")
			}
			p, err := findLatestKubeletProviderPipeline(client, projectID, ref)
			if err != nil {
				return err
			}
			if p == nil {
				return fmt.Errorf("no kubelet-provider pipelines found for ref=%s", ref)
			}
			pipelineID = p.ID
		}

		jobs, err := client.listPipelineJobs(projectID, pipelineID)
		if err != nil {
			return err
		}
		kubeletJob := findJobByName(jobs, kubeletProviderJobName)
		if kubeletJob == nil {
			return fmt.Errorf("pipeline #%d has no %s job", pipelineID, kubeletProviderJobName)
		}
		jobID = kubeletJob.ID
	}

	trace, err := client.getJobTrace(projectID, jobID)
	if err != nil {
		return err
	}
	fmt.Print(tailLines(trace, tail))
	return nil
}

func newGitLabClient(baseURL, token string, timeout time.Duration) (*gitLabClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" {
		return nil, errors.New("gitlab url is empty")
	}
	if token == "" {
		return nil, errors.New("gitlab token is empty")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid gitlab url: %w", err)
	}

	return &gitLabClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *gitLabClient) createPipeline(projectID int, payload gitLabCreatePipelineRequest) (*gitLabPipeline, error) {
	var out gitLabPipeline
	if err := c.requestJSON(http.MethodPost, fmt.Sprintf("/api/v4/projects/%d/pipeline", projectID), payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *gitLabClient) getPipeline(projectID, pipelineID int) (*gitLabPipeline, error) {
	var out gitLabPipeline
	if err := c.requestJSON(http.MethodGet, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d", projectID, pipelineID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *gitLabClient) listPipelines(projectID int, ref string, perPage int) ([]gitLabPipeline, error) {
	if perPage <= 0 {
		perPage = 20
	}
	query := url.Values{}
	if strings.TrimSpace(ref) != "" {
		query.Set("ref", ref)
	}
	query.Set("per_page", strconv.Itoa(perPage))

	var out []gitLabPipeline
	path := fmt.Sprintf("/api/v4/projects/%d/pipelines?%s", projectID, query.Encode())
	if err := c.requestJSON(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gitLabClient) listPipelinesByProject(project string, ref string, perPage int) ([]gitLabPipeline, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, errors.New("project is empty")
	}
	if perPage <= 0 {
		perPage = 20
	}
	query := url.Values{}
	if strings.TrimSpace(ref) != "" {
		query.Set("ref", ref)
	}
	query.Set("per_page", strconv.Itoa(perPage))

	var out []gitLabPipeline
	path := fmt.Sprintf("/api/v4/projects/%s/pipelines?%s", url.PathEscape(project), query.Encode())
	if err := c.requestJSON(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gitLabClient) listMergeRequestsByProject(project string, sourceBranch string, state string, perPage int) ([]gitLabMergeRequest, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, errors.New("project is empty")
	}
	if perPage <= 0 {
		perPage = 20
	}
	query := url.Values{}
	if strings.TrimSpace(sourceBranch) != "" {
		query.Set("source_branch", sourceBranch)
	}
	if strings.TrimSpace(state) != "" {
		query.Set("state", state)
	}
	query.Set("order_by", "updated_at")
	query.Set("sort", "desc")
	query.Set("per_page", strconv.Itoa(perPage))

	var out []gitLabMergeRequest
	path := fmt.Sprintf("/api/v4/projects/%s/merge_requests?%s", url.PathEscape(project), query.Encode())
	if err := c.requestJSON(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gitLabClient) mergeMergeRequestByProject(project string, mergeRequestIID int, removeSourceBranch bool) (*gitLabMergeRequest, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, errors.New("project is empty")
	}
	if mergeRequestIID <= 0 {
		return nil, errors.New("merge request iid must be > 0")
	}

	payload := map[string]any{
		"should_remove_source_branch": removeSourceBranch,
	}
	var out gitLabMergeRequest
	path := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d/merge", url.PathEscape(project), mergeRequestIID)
	if err := c.requestJSON(http.MethodPut, path, payload, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *gitLabClient) getMergeRequestByProject(project string, mergeRequestIID int, includeRebaseInProgress bool) (*gitLabMergeRequest, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, errors.New("project is empty")
	}
	if mergeRequestIID <= 0 {
		return nil, errors.New("merge request iid must be > 0")
	}

	path := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d", url.PathEscape(project), mergeRequestIID)
	if includeRebaseInProgress {
		path += "?include_rebase_in_progress=true"
	}

	var out gitLabMergeRequest
	if err := c.requestJSON(http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *gitLabClient) rebaseMergeRequestByProject(project string, mergeRequestIID int, skipCI bool) error {
	project = strings.TrimSpace(project)
	if project == "" {
		return errors.New("project is empty")
	}
	if mergeRequestIID <= 0 {
		return errors.New("merge request iid must be > 0")
	}

	path := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d/rebase", url.PathEscape(project), mergeRequestIID)
	if skipCI {
		path += "?skip_ci=true"
	}
	return c.requestJSON(http.MethodPut, path, nil, nil)
}

func (c *gitLabClient) listPipelineJobs(projectID, pipelineID int) ([]gitLabJob, error) {
	var out []gitLabJob
	if err := c.requestJSON(http.MethodGet, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d/jobs?per_page=100", projectID, pipelineID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gitLabClient) getJobTrace(projectID, jobID int) (string, error) {
	req, err := c.newRequest(http.MethodGet, fmt.Sprintf("/api/v4/projects/%d/jobs/%d/trace", projectID, jobID), nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gitlab api %s: HTTP %d: %s", req.URL.String(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return string(body), nil
}

func (c *gitLabClient) cancelPipeline(projectID, pipelineID int) error {
	req, err := c.newRequest(http.MethodPost, fmt.Sprintf("/api/v4/projects/%d/pipelines/%d/cancel", projectID, pipelineID), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cancel pipeline failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *gitLabClient) requestJSON(method, path string, requestBody any, out any) error {
	var bodyReader io.Reader
	if requestBody != nil {
		raw, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(raw)
	}

	req, err := c.newRequest(method, path, bodyReader)
	if err != nil {
		return err
	}
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("gitlab api %s: HTTP %d: %s", req.URL.String(), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response JSON: %w", err)
	}
	return nil
}

func (c *gitLabClient) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func findLatestKubeletProviderPipeline(client *gitLabClient, projectID int, ref string) (*gitLabPipeline, error) {
	pipelines, err := client.listPipelines(projectID, ref, 50)
	if err != nil {
		return nil, err
	}

	for _, p := range pipelines {
		jobs, err := client.listPipelineJobs(projectID, p.ID)
		if err != nil {
			continue
		}
		if findJobByName(jobs, kubeletProviderJobName) != nil {
			pCopy := p
			return &pCopy, nil
		}
	}
	return nil, nil
}

func findLatestRunningKubeletProviderPipeline(client *gitLabClient, projectID int, ref string) (*gitLabPipeline, error) {
	pipelines, err := client.listPipelines(projectID, ref, 50)
	if err != nil {
		return nil, err
	}

	for _, p := range pipelines {
		if !isRunningPipelineStatus(p.Status) {
			continue
		}
		jobs, err := client.listPipelineJobs(projectID, p.ID)
		if err != nil {
			continue
		}
		job := findJobByName(jobs, kubeletProviderJobName)
		if job != nil && isRunningJobStatus(job.Status) {
			pCopy := p
			return &pCopy, nil
		}
	}
	return nil, nil
}

func findJobByName(jobs []gitLabJob, name string) *gitLabJob {
	for i := range jobs {
		if jobs[i].Name == name {
			return &jobs[i]
		}
	}
	return nil
}

func isTerminalPipelineStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "failed", "canceled", "skipped", "manual":
		return true
	default:
		return false
	}
}

func isRunningPipelineStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "waiting_for_resource", "preparing", "pending", "running":
		return true
	default:
		return false
	}
}

func isRunningJobStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "pending", "running", "preparing", "waiting_for_resource", "canceling":
		return true
	default:
		return false
	}
}

func tailLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) <= maxLines {
		return text
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		val := strings.TrimSpace(os.Getenv(key))
		if val != "" {
			return val
		}
	}
	return ""
}
