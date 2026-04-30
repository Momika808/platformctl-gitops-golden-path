package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type newAppAutoConfig struct {
	RepoRoot            string
	Layer               string
	Namespace           string
	App                 string
	K8sRemote           string
	K8sBaseBranch       string
	K8sBranch           string
	K8sCommitMessage    string
	K8sMRTitle          string
	K8sMRDescription    string
	K8sProject          string
	K8sGitLabURL        string
	K8sGitLabToken      string
	K8sPaths            []string
	VaultProject        string
	VaultBranch         string
	VaultMRURL          string
	VaultGitLabURL      string
	VaultGitLabToken    string
	WaitCI              bool
	CITimeout           time.Duration
	CIPollInterval      time.Duration
	AutoMerge           bool
	RemoveSourceBranch  bool
	VerifyVault         bool
	VaultAddr           string
	VaultToken          string
	VaultRole           string
	VaultPolicy         string
	VaultRequestTimeout time.Duration
	VaultVerifyTimeout  time.Duration
	VaultVerifyPoll     time.Duration
}

type autoMRResult struct {
	Branch string
	MRURL  string
}

const (
	autoMergeRebaseTimeout = 3 * time.Minute
	autoMergeRebasePoll    = 3 * time.Second
)

func runNewAppAuto(cfg newAppAutoConfig) error {
	startedAt := time.Now().UTC()
	logInfo("new_app_auto_start", map[string]any{
		"component": "new-app-auto",
		"layer":     cfg.Layer,
		"namespace": cfg.Namespace,
		"app":       cfg.App,
		"k8s_ref":   cfg.K8sBaseBranch,
		"vault_ref": cfg.VaultBranch,
	})

	if strings.TrimSpace(cfg.RepoRoot) == "" {
		return errors.New("auto mode: repo root is required")
	}
	if strings.TrimSpace(cfg.K8sGitLabToken) == "" {
		return errors.New("auto mode: k8s GitLab token is required")
	}
	if strings.TrimSpace(cfg.VaultGitLabToken) == "" {
		return errors.New("auto mode: vault GitLab token is required")
	}
	if strings.TrimSpace(cfg.VaultBranch) == "" {
		return errors.New("auto mode: vault branch is required (enable vault MR scaffold first)")
	}
	if cfg.AutoMerge && !cfg.WaitCI {
		return errors.New("auto mode: auto merge requires CI wait")
	}
	if cfg.VerifyVault {
		if strings.TrimSpace(cfg.VaultAddr) == "" || strings.TrimSpace(cfg.VaultToken) == "" {
			return errors.New("auto mode: Vault address and token are required for auto Vault verification")
		}
		if strings.TrimSpace(cfg.VaultRole) == "" || strings.TrimSpace(cfg.VaultPolicy) == "" {
			return errors.New("auto mode: Vault role/policy are required for auto Vault verification")
		}
	}

	k8sResult, err := createK8sMergeRequestForNewApp(cfg)
	if err != nil {
		logError("new_app_auto_failed_create_k8s_mr", map[string]any{
			"component": "new-app-auto",
			"error":     err.Error(),
		})
		emitAlertIfConfigured("platformctl new-app auto failed", map[string]any{
			"component": "new-app-auto",
			"layer":     cfg.Layer,
			"namespace": cfg.Namespace,
			"error":     err.Error(),
		})
		return err
	}
	fmt.Printf("Created k8s merge request: %s\n", k8sResult.MRURL)
	logInfo("new_app_auto_k8s_mr_created", map[string]any{
		"component": "new-app-auto",
		"mr_url":    k8sResult.MRURL,
		"branch":    k8sResult.Branch,
	})
	if cfg.VaultMRURL != "" {
		fmt.Printf("Vault MR already created: %s\n", cfg.VaultMRURL)
		logInfo("new_app_auto_vault_mr_created", map[string]any{
			"component": "new-app-auto",
			"mr_url":    cfg.VaultMRURL,
			"branch":    cfg.VaultBranch,
		})
	}

	if cfg.CITimeout <= 0 {
		cfg.CITimeout = 45 * time.Minute
	}
	if cfg.CIPollInterval <= 0 {
		cfg.CIPollInterval = 10 * time.Second
	}
	if cfg.VaultRequestTimeout <= 0 {
		cfg.VaultRequestTimeout = 15 * time.Second
	}
	if cfg.VaultVerifyTimeout <= 0 {
		cfg.VaultVerifyTimeout = 10 * time.Minute
	}
	if cfg.VaultVerifyPoll <= 0 {
		cfg.VaultVerifyPoll = 10 * time.Second
	}

	if !cfg.WaitCI && !cfg.AutoMerge {
		return nil
	}

	vaultClient, err := newGitLabClient(cfg.VaultGitLabURL, cfg.VaultGitLabToken, 15*time.Second)
	if err != nil {
		return fmt.Errorf("auto mode: init vault GitLab client: %w", err)
	}
	k8sClient, err := newGitLabClient(cfg.K8sGitLabURL, cfg.K8sGitLabToken, 15*time.Second)
	if err != nil {
		return fmt.Errorf("auto mode: init k8s GitLab client: %w", err)
	}

	if cfg.WaitCI {
		fmt.Println("Waiting vault-control-plane CI...")
		vaultPipeline, err := waitForBranchPipelineSuccess(vaultClient, cfg.VaultProject, cfg.VaultBranch, cfg.CITimeout, cfg.CIPollInterval)
		if err != nil {
			logError("new_app_auto_vault_ci_failed", map[string]any{
				"component": "new-app-auto",
				"project":   cfg.VaultProject,
				"branch":    cfg.VaultBranch,
				"error":     err.Error(),
			})
			emitAlertIfConfigured("platformctl new-app auto: vault CI failed", map[string]any{
				"component": "new-app-auto",
				"layer":     cfg.Layer,
				"namespace": cfg.Namespace,
				"project":   cfg.VaultProject,
				"branch":    cfg.VaultBranch,
				"error":     err.Error(),
			})
			return fmt.Errorf("vault-control-plane CI failed: %w", err)
		}
		fmt.Printf("vault-control-plane pipeline #%d passed: %s\n", vaultPipeline.ID, vaultPipeline.WebURL)

		fmt.Println("Waiting k8s CI...")
		k8sPipeline, err := waitForBranchPipelineSuccess(k8sClient, cfg.K8sProject, k8sResult.Branch, cfg.CITimeout, cfg.CIPollInterval)
		if err != nil {
			logError("new_app_auto_k8s_ci_failed", map[string]any{
				"component": "new-app-auto",
				"project":   cfg.K8sProject,
				"branch":    k8sResult.Branch,
				"error":     err.Error(),
			})
			emitAlertIfConfigured("platformctl new-app auto: k8s CI failed", map[string]any{
				"component": "new-app-auto",
				"layer":     cfg.Layer,
				"namespace": cfg.Namespace,
				"project":   cfg.K8sProject,
				"branch":    k8sResult.Branch,
				"error":     err.Error(),
			})
			return fmt.Errorf("k8s CI failed: %w", err)
		}
		fmt.Printf("k8s pipeline #%d passed: %s\n", k8sPipeline.ID, k8sPipeline.WebURL)
	}

	if !cfg.AutoMerge {
		fmt.Println("Auto onboarding safe-mode completed (MRs created, CI green, no merge executed).")
		return nil
	}

	fmt.Println("Merging vault-control-plane merge request...")
	vaultMR, err := mergeMergeRequestBySourceBranch(vaultClient, cfg.VaultProject, cfg.VaultBranch, cfg.RemoveSourceBranch)
	if err != nil {
		logError("new_app_auto_vault_merge_failed", map[string]any{
			"component": "new-app-auto",
			"project":   cfg.VaultProject,
			"branch":    cfg.VaultBranch,
			"error":     err.Error(),
		})
		return fmt.Errorf("auto mode: merge vault-control-plane MR failed: %w", err)
	}
	fmt.Printf("vault-control-plane MR state=%s url=%s\n", vaultMR.State, vaultMR.WebURL)

	if cfg.VerifyVault {
		fmt.Println("Verifying Vault role/policy runtime state...")
		if err := waitForVaultRoleAndPolicy(cfg); err != nil {
			return fmt.Errorf("auto mode: verify Vault after merge: %w", err)
		}
		fmt.Println("Vault verification passed.")
	}

	fmt.Println("Merging k8s merge request...")
	k8sMR, err := mergeMergeRequestBySourceBranch(k8sClient, cfg.K8sProject, k8sResult.Branch, cfg.RemoveSourceBranch)
	if err != nil {
		logError("new_app_auto_k8s_merge_failed", map[string]any{
			"component": "new-app-auto",
			"project":   cfg.K8sProject,
			"branch":    k8sResult.Branch,
			"error":     err.Error(),
		})
		emitAlertIfConfigured("platformctl new-app auto: k8s merge failed", map[string]any{
			"component": "new-app-auto",
			"layer":     cfg.Layer,
			"namespace": cfg.Namespace,
			"project":   cfg.K8sProject,
			"branch":    k8sResult.Branch,
			"error":     err.Error(),
		})
		return fmt.Errorf("auto mode: merge k8s MR failed: %w", err)
	}
	fmt.Printf("k8s MR state=%s url=%s\n", k8sMR.State, k8sMR.WebURL)

	fmt.Println("Auto onboarding completed (vault merged+verified, k8s merged).")
	logInfo("new_app_auto_completed", map[string]any{
		"component":    "new-app-auto",
		"layer":        cfg.Layer,
		"namespace":    cfg.Namespace,
		"elapsed_ms":   time.Since(startedAt).Milliseconds(),
		"k8s_mr_url":   k8sMR.WebURL,
		"vault_mr_url": vaultMR.WebURL,
	})
	return nil
}

func createK8sMergeRequestForNewApp(cfg newAppAutoConfig) (autoMRResult, error) {
	var result autoMRResult

	if strings.TrimSpace(cfg.K8sRemote) == "" {
		cfg.K8sRemote = "origin"
	}
	if strings.TrimSpace(cfg.K8sBaseBranch) == "" {
		cfg.K8sBaseBranch = "master"
	}

	branch := strings.TrimSpace(cfg.K8sBranch)
	if branch == "" {
		branch = fmt.Sprintf("platformctl/new-app-%s-%s-k8s", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	}

	originalBranch, _ := gitCurrentBranch(cfg.RepoRoot)
	if err := runGit(cfg.RepoRoot, "fetch", cfg.K8sRemote, cfg.K8sBaseBranch); err != nil {
		return result, fmt.Errorf("auto mode: git fetch k8s failed: %w", err)
	}
	if err := runGit(cfg.RepoRoot, "checkout", "-B", branch, cfg.K8sRemote+"/"+cfg.K8sBaseBranch); err != nil {
		return result, fmt.Errorf("auto mode: git checkout k8s branch failed: %w", err)
	}

	defer func() {
		if originalBranch != "" && originalBranch != "HEAD" && originalBranch != branch {
			_ = runGit(cfg.RepoRoot, "checkout", originalBranch)
		}
	}()

	for _, rel := range cfg.K8sPaths {
		if strings.TrimSpace(rel) == "" {
			continue
		}
		path := filepath.FromSlash(rel)
		if _, err := os.Stat(filepath.Join(cfg.RepoRoot, path)); err != nil {
			return result, fmt.Errorf("auto mode: k8s path not found: %s", rel)
		}
		if err := runGit(cfg.RepoRoot, "add", filepath.ToSlash(path)); err != nil {
			return result, fmt.Errorf("auto mode: git add %s failed: %w", rel, err)
		}
	}

	dirty, err := gitHasStagedChanges(cfg.RepoRoot)
	if err != nil {
		return result, fmt.Errorf("auto mode: check staged changes failed: %w", err)
	}
	if !dirty {
		return result, errors.New("auto mode: no staged changes for k8s merge request")
	}

	commitMessage := strings.TrimSpace(cfg.K8sCommitMessage)
	if commitMessage == "" {
		commitMessage = fmt.Sprintf("k8s: add app layer %s (%s)", cfg.Layer, cfg.Namespace)
	}
	if err := runGit(cfg.RepoRoot, "commit", "-m", commitMessage); err != nil {
		return result, fmt.Errorf("auto mode: git commit k8s failed: %w", err)
	}
	if err := runGit(cfg.RepoRoot, "push", "-u", cfg.K8sRemote, branch); err != nil {
		return result, fmt.Errorf("auto mode: git push k8s failed: %w", err)
	}

	mrTitle := strings.TrimSpace(cfg.K8sMRTitle)
	if mrTitle == "" {
		mrTitle = fmt.Sprintf("k8s: bootstrap app layer %s", cfg.Layer)
	}
	mrDescription := strings.TrimSpace(cfg.K8sMRDescription)
	if mrDescription == "" {
		mrDescription = fmt.Sprintf("Generated by platformctl new-app --auto for namespace `%s`.", cfg.Namespace)
	}

	mrURL, err := createGitLabMR(cfg.K8sGitLabURL, cfg.K8sGitLabToken, cfg.K8sProject, branch, cfg.K8sBaseBranch, mrTitle, mrDescription)
	if err != nil {
		return result, fmt.Errorf("auto mode: create k8s MR failed: %w", err)
	}

	result.Branch = branch
	result.MRURL = mrURL
	return result, nil
}

func waitForBranchPipelineSuccess(client *gitLabClient, project string, branch string, timeout time.Duration, pollInterval time.Duration) (*gitLabPipeline, error) {
	projectID := strings.TrimSpace(project)
	if projectID == "" {
		return nil, errors.New("project is empty")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil, errors.New("branch is empty")
	}

	deadline := time.Now().Add(timeout)
	var lastSeen *gitLabPipeline
	var lastStatus string
	sameStatusPolls := 0

	for {
		if time.Now().After(deadline) {
			if lastSeen != nil {
				logError("pipeline_wait_timeout", map[string]any{
					"component": "pipeline-wait",
					"project":   projectID,
					"branch":    branch,
					"pipeline":  lastSeen.ID,
					"status":    lastSeen.Status,
					"url":       lastSeen.WebURL,
				})
				emitAlertIfConfigured("platformctl pipeline wait timeout", map[string]any{
					"project":  projectID,
					"branch":   branch,
					"pipeline": lastSeen.ID,
					"status":   lastSeen.Status,
					"url":      lastSeen.WebURL,
				})
				return nil, fmt.Errorf("timeout waiting for pipeline on branch %s (last status=%s, url=%s)", branch, lastSeen.Status, lastSeen.WebURL)
			}
			logError("pipeline_wait_timeout", map[string]any{
				"component": "pipeline-wait",
				"project":   projectID,
				"branch":    branch,
			})
			return nil, fmt.Errorf("timeout waiting for pipeline on branch %s", branch)
		}

		pipelines, err := client.listPipelinesByProject(projectID, branch, 20)
		if err != nil {
			return nil, err
		}
		if len(pipelines) == 0 {
			time.Sleep(pollInterval)
			continue
		}

		p := pipelines[0]
		lastSeen = &p
		fmt.Printf("  pipeline #%d status=%s\n", p.ID, p.Status)
		if p.Status == lastStatus {
			sameStatusPolls++
		} else {
			sameStatusPolls = 0
		}
		lastStatus = p.Status
		if sameStatusPolls == 0 || sameStatusPolls%12 == 0 {
			logInfo("pipeline_poll", map[string]any{
				"component":        "pipeline-wait",
				"project":          projectID,
				"branch":           branch,
				"pipeline":         p.ID,
				"status":           p.Status,
				"same_status_hits": sameStatusPolls,
				"url":              p.WebURL,
			})
		}

		if isTerminalPipelineStatus(p.Status) {
			if strings.EqualFold(p.Status, "success") {
				logInfo("pipeline_success", map[string]any{
					"component": "pipeline-wait",
					"project":   projectID,
					"branch":    branch,
					"pipeline":  p.ID,
					"url":       p.WebURL,
				})
				return &p, nil
			}
			logError("pipeline_failed", map[string]any{
				"component": "pipeline-wait",
				"project":   projectID,
				"branch":    branch,
				"pipeline":  p.ID,
				"status":    p.Status,
				"url":       p.WebURL,
			})
			emitAlertIfConfigured("platformctl pipeline failed", map[string]any{
				"project":  projectID,
				"branch":   branch,
				"pipeline": p.ID,
				"status":   p.Status,
				"url":      p.WebURL,
			})
			return &p, fmt.Errorf("pipeline #%d ended with status=%s (%s)", p.ID, p.Status, p.WebURL)
		}

		time.Sleep(pollInterval)
	}
}

func mergeMergeRequestBySourceBranch(client *gitLabClient, project string, branch string, removeSourceBranch bool) (*gitLabMergeRequest, error) {
	const maxMergeAttempts = 2

	for attempt := 1; attempt <= maxMergeAttempts; attempt++ {
		mr, err := latestMergeRequestForBranch(client, project, branch)
		if err != nil {
			return nil, err
		}
		if mr == nil {
			return nil, fmt.Errorf("merge request not found for project=%s branch=%s", project, branch)
		}

		switch strings.ToLower(strings.TrimSpace(mr.State)) {
		case "merged":
			return mr, nil
		case "opened":
			merged, err := client.mergeMergeRequestByProject(project, mr.IID, removeSourceBranch)
			if err == nil {
				return merged, nil
			}
			if attempt >= maxMergeAttempts || !shouldAttemptAutoRebase(mr, err) {
				return nil, err
			}

			fmt.Printf("MR !%d is not mergeable yet; attempting auto-rebase...\n", mr.IID)
			logWarn("auto_merge_rebase_retry", map[string]any{
				"component":             "mr-merge",
				"project":               project,
				"branch":                branch,
				"mr_iid":                mr.IID,
				"merge_status":          mr.MergeStatus,
				"detailed_merge_status": mr.DetailedMergeState,
				"has_conflicts":         mr.HasConflicts,
				"error":                 err.Error(),
				"attempt":               attempt,
			})
			if err := autoRebaseMergeRequest(client, project, mr.IID, autoMergeRebaseTimeout, autoMergeRebasePoll); err != nil {
				return nil, fmt.Errorf("merge request !%d auto-rebase failed: %w", mr.IID, err)
			}
		default:
			return nil, fmt.Errorf("merge request #%d is not mergeable, state=%s", mr.IID, mr.State)
		}
	}

	return nil, fmt.Errorf("merge request merge failed for project=%s branch=%s after retries", project, branch)
}

func shouldAttemptAutoRebase(mr *gitLabMergeRequest, mergeErr error) bool {
	if mr != nil {
		detailed := strings.ToLower(strings.TrimSpace(mr.DetailedMergeState))
		mergeStatus := strings.ToLower(strings.TrimSpace(mr.MergeStatus))
		if mr.HasConflicts ||
			strings.Contains(detailed, "conflict") ||
			strings.Contains(detailed, "behind") ||
			strings.Contains(detailed, "need_rebase") ||
			strings.Contains(mergeStatus, "cannot_be_merged") ||
			strings.Contains(mergeStatus, "unchecked") {
			return true
		}
	}

	if mergeErr == nil {
		return false
	}
	msg := strings.ToLower(mergeErr.Error())
	indicators := []string{
		"http 405",
		"method not allowed",
		"cannot be merged",
		"merge request is not mergeable",
		"source branch is behind",
		"has conflicts",
		"merge conflict",
	}
	for _, indicator := range indicators {
		if strings.Contains(msg, indicator) {
			return true
		}
	}
	return false
}

func autoRebaseMergeRequest(client *gitLabClient, project string, mergeRequestIID int, timeout time.Duration, pollInterval time.Duration) error {
	if timeout <= 0 {
		timeout = autoMergeRebaseTimeout
	}
	if pollInterval <= 0 {
		pollInterval = autoMergeRebasePoll
	}

	if err := client.rebaseMergeRequestByProject(project, mergeRequestIID, true); err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for {
		currentMR, err := client.getMergeRequestByProject(project, mergeRequestIID, true)
		if err != nil {
			return fmt.Errorf("read merge request after rebase: %w", err)
		}
		if !currentMR.RebaseInProgress {
			if strings.TrimSpace(currentMR.MergeError) != "" {
				return fmt.Errorf("gitlab rebase error: %s", currentMR.MergeError)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for rebase completion for merge request !%d", mergeRequestIID)
		}
		time.Sleep(pollInterval)
	}
}

func latestMergeRequestForBranch(client *gitLabClient, project string, branch string) (*gitLabMergeRequest, error) {
	mrs, err := client.listMergeRequestsByProject(project, branch, "all", 20)
	if err != nil {
		return nil, err
	}
	if len(mrs) == 0 {
		return nil, nil
	}
	mr := mrs[0]
	return &mr, nil
}

func waitForVaultRoleAndPolicy(cfg newAppAutoConfig) error {
	client, err := newVaultClient(cfg.VaultAddr, cfg.VaultToken, cfg.VaultRequestTimeout)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(cfg.VaultVerifyTimeout)
	var lastErr error

	for {
		roleErr := client.ensureKubernetesRoleExists(cfg.VaultRole)
		policyErr := client.ensurePolicyExists(cfg.VaultPolicy)

		if roleErr == nil && policyErr == nil {
			return nil
		}
		if isVaultAuthError(roleErr) || isVaultAuthError(policyErr) {
			if roleErr != nil {
				return roleErr
			}
			return policyErr
		}

		lastErr = combineVaultVerifyErrors(roleErr, policyErr)
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for Vault role/policy: %w", lastErr)
		}
		time.Sleep(cfg.VaultVerifyPoll)
	}
}

func combineVaultVerifyErrors(roleErr error, policyErr error) error {
	switch {
	case roleErr != nil && policyErr != nil:
		return fmt.Errorf("role check: %v; policy check: %v", roleErr, policyErr)
	case roleErr != nil:
		return fmt.Errorf("role check: %w", roleErr)
	default:
		return fmt.Errorf("policy check: %w", policyErr)
	}
}

func isVaultAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP 401") || strings.Contains(msg, "HTTP 403")
}

type vaultClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func newVaultClient(baseURL string, token string, timeout time.Duration) (*vaultClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" {
		return nil, errors.New("vault addr is empty")
	}
	if token == "" {
		return nil, errors.New("vault token is empty")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("invalid vault addr: %w", err)
	}

	return &vaultClient{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *vaultClient) ensureKubernetesRoleExists(role string) error {
	path := "/v1/auth/kubernetes/role/" + url.PathEscape(strings.TrimSpace(role))
	statusCode, body, err := c.get(path)
	if err != nil {
		return err
	}
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	return fmt.Errorf("Vault role %q not ready: HTTP %d: %s", role, statusCode, body)
}

func (c *vaultClient) ensurePolicyExists(policy string) error {
	policy = strings.TrimSpace(policy)
	primary := "/v1/sys/policies/acl/" + url.PathEscape(policy)
	statusCode, body, err := c.get(primary)
	if err != nil {
		return err
	}
	if statusCode >= 200 && statusCode < 300 {
		return nil
	}
	if statusCode == http.StatusNotFound {
		fallback := "/v1/sys/policy/" + url.PathEscape(policy)
		fallbackCode, fallbackBody, fallbackErr := c.get(fallback)
		if fallbackErr != nil {
			return fallbackErr
		}
		if fallbackCode >= 200 && fallbackCode < 300 {
			return nil
		}
		return fmt.Errorf("Vault policy %q not ready: HTTP %d: %s", policy, fallbackCode, fallbackBody)
	}
	return fmt.Errorf("Vault policy %q not ready: HTTP %d: %s", policy, statusCode, body)
}

func (c *vaultClient) get(path string) (int, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("X-Vault-Token", c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}
