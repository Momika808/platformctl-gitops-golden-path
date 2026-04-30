package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type vaultScaffoldConfig struct {
	VaultRepoRoot   string
	RoleName        string
	ServiceAccount  string
	Namespace       string
	SecretPath      string
	EnableMR        bool
	Remote          string
	BaseBranch      string
	Branch          string
	CommitMessage   string
	MRTitle         string
	MRDescription   string
	GitLabProject   string
	GitLabURL       string
	GitLabToken     string
	SourceLayerName string
}

type vaultScaffoldResult struct {
	Created []string
	Updated []string
	MRURL   string
}

type vaultRoleDefinition struct {
	Name                          string   `yaml:"name"`
	PolicyFile                    string   `yaml:"policy_file"`
	BoundServiceAccountNames      []string `yaml:"bound_service_account_names"`
	BoundServiceAccountNamespaces []string `yaml:"bound_service_account_namespaces"`
	TokenType                     string   `yaml:"token_type,omitempty"`
}

func scaffoldVaultControlPlane(cfg vaultScaffoldConfig) (vaultScaffoldResult, error) {
	var result vaultScaffoldResult

	if strings.TrimSpace(cfg.VaultRepoRoot) == "" {
		return result, errors.New("vault scaffold: VaultRepoRoot is required")
	}
	if strings.TrimSpace(cfg.RoleName) == "" || strings.TrimSpace(cfg.Namespace) == "" || strings.TrimSpace(cfg.ServiceAccount) == "" {
		return result, errors.New("vault scaffold: RoleName, Namespace and ServiceAccount are required")
	}
	if strings.TrimSpace(cfg.SecretPath) == "" {
		return result, errors.New("vault scaffold: SecretPath is required")
	}

	vaultRoot := cfg.VaultRepoRoot
	if !filepath.IsAbs(vaultRoot) {
		abs, err := filepath.Abs(vaultRoot)
		if err != nil {
			return result, fmt.Errorf("vault scaffold: resolve vault repo root: %w", err)
		}
		vaultRoot = abs
	}

	if _, err := os.Stat(filepath.Join(vaultRoot, ".git")); err != nil {
		return result, fmt.Errorf("vault scaffold: not a git repository: %s", vaultRoot)
	}
	if _, err := os.Stat(filepath.Join(vaultRoot, "main.tf")); err != nil {
		return result, fmt.Errorf("vault scaffold: main.tf not found: %s", vaultRoot)
	}

	rolesDir := filepath.Join(vaultRoot, "roles.d")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		return result, fmt.Errorf("vault scaffold: create roles.d: %w", err)
	}
	policiesDir := filepath.Join(vaultRoot, "policies")
	if err := os.MkdirAll(policiesDir, 0o755); err != nil {
		return result, fmt.Errorf("vault scaffold: create policies dir: %w", err)
	}

	roleDoc := vaultRoleDefinition{
		Name:                          cfg.RoleName,
		PolicyFile:                    fmt.Sprintf("policies/%s.hcl", cfg.RoleName),
		BoundServiceAccountNames:      []string{cfg.ServiceAccount},
		BoundServiceAccountNamespaces: []string{cfg.Namespace},
		TokenType:                     "batch",
	}
	roleBody, err := marshalYAMLWithIndent(roleDoc, 2)
	if err != nil {
		return result, fmt.Errorf("vault scaffold: marshal role YAML: %w", err)
	}
	roleFile := filepath.Join(rolesDir, cfg.RoleName+".yaml")
	roleStatus, err := writeScaffoldFile(roleFile, roleBody)
	if err != nil {
		return result, err
	}
	if roleStatus == "created" {
		result.Created = append(result.Created, roleFile)
	}
	if roleStatus == "updated" {
		result.Updated = append(result.Updated, roleFile)
	}

	policyBody := []byte(fmt.Sprintf("path \"kv/data/%s\" {\n  capabilities = [\"read\"]\n}\n", cfg.SecretPath))
	policyFile := filepath.Join(policiesDir, cfg.RoleName+".hcl")
	policyStatus, err := writeScaffoldFile(policyFile, policyBody)
	if err != nil {
		return result, err
	}
	if policyStatus == "created" {
		result.Created = append(result.Created, policyFile)
	}
	if policyStatus == "updated" {
		result.Updated = append(result.Updated, policyFile)
	}

	if !cfg.EnableMR {
		return result, nil
	}

	mrURL, err := createVaultMergeRequest(vaultRoot, cfg, len(result.Created)+len(result.Updated) > 0)
	if err != nil {
		return result, err
	}
	result.MRURL = mrURL
	return result, nil
}

func writeScaffoldFile(path string, content []byte) (string, error) {
	if current, err := os.ReadFile(path); err == nil {
		if normalizeText(string(current)) == normalizeText(string(content)) {
			return "unchanged", nil
		}
		return "", fmt.Errorf("vault scaffold: file already exists with different content: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", err
	}
	return "created", nil
}

func normalizeText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

func createVaultMergeRequest(vaultRoot string, cfg vaultScaffoldConfig, hasChanges bool) (string, error) {
	if strings.TrimSpace(cfg.GitLabToken) == "" {
		return "", errors.New("vault scaffold: --with-vault-mr requires --vault-gitlab-token or PLATFORM_GITLAB_TOKEN")
	}
	if !hasChanges {
		return "", nil
	}

	if cfg.Remote == "" {
		cfg.Remote = "origin"
	}
	if cfg.BaseBranch == "" {
		cfg.BaseBranch = "main"
	}
	branch := strings.TrimSpace(cfg.Branch)
	if branch == "" {
		branch = fmt.Sprintf("platformctl/new-app-%s-%s", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	}
	originalBranch, _ := gitCurrentBranch(vaultRoot)
	if originalBranch != "" && originalBranch != "HEAD" {
		defer func() {
			if originalBranch != branch {
				_ = runGit(vaultRoot, "checkout", originalBranch)
			}
		}()
	}

	if err := runGit(vaultRoot, "fetch", cfg.Remote, cfg.BaseBranch); err != nil {
		return "", fmt.Errorf("vault scaffold: git fetch failed: %w", err)
	}
	if err := runGit(vaultRoot, "checkout", "-B", branch, cfg.Remote+"/"+cfg.BaseBranch); err != nil {
		return "", fmt.Errorf("vault scaffold: git checkout failed: %w", err)
	}

	roleRel := filepath.ToSlash(filepath.Join("roles.d", cfg.RoleName+".yaml"))
	policyRel := filepath.ToSlash(filepath.Join("policies", cfg.RoleName+".hcl"))
	if err := runGit(vaultRoot, "add", roleRel, policyRel); err != nil {
		return "", fmt.Errorf("vault scaffold: git add failed: %w", err)
	}

	dirty, err := gitHasStagedChanges(vaultRoot)
	if err != nil {
		return "", fmt.Errorf("vault scaffold: check staged changes failed: %w", err)
	}
	if dirty {
		commitMessage := strings.TrimSpace(cfg.CommitMessage)
		if commitMessage == "" {
			commitMessage = fmt.Sprintf("vault: add role/policy for %s", cfg.Namespace)
		}
		if err := runGit(vaultRoot, "commit", "-m", commitMessage); err != nil {
			return "", fmt.Errorf("vault scaffold: git commit failed: %w", err)
		}
	}

	if err := runGit(vaultRoot, "push", "-u", cfg.Remote, branch); err != nil {
		return "", fmt.Errorf("vault scaffold: git push failed: %w", err)
	}

	mrTitle := strings.TrimSpace(cfg.MRTitle)
	if mrTitle == "" {
		mrTitle = fmt.Sprintf("vault: bootstrap role/policy for %s", cfg.Namespace)
	}
	mrDescription := strings.TrimSpace(cfg.MRDescription)
	if mrDescription == "" {
		mrDescription = fmt.Sprintf("Generated by platformctl new-app for layer `%s` and namespace `%s`.", cfg.SourceLayerName, cfg.Namespace)
	}

	mrURL, err := createGitLabMR(cfg.GitLabURL, cfg.GitLabToken, cfg.GitLabProject, branch, cfg.BaseBranch, mrTitle, mrDescription)
	if err != nil {
		return "", err
	}
	return mrURL, nil
}

func runGit(repoRoot string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitHasStagedChanges(repoRoot string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = repoRoot
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, err
}

func gitCurrentBranch(repoRoot string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func createGitLabMR(baseURL, token, project, sourceBranch, targetBranch, title, description string) (string, error) {
	projectID := url.PathEscape(strings.TrimSpace(project))
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v4/projects/" + projectID + "/merge_requests"

	payload := map[string]any{
		"source_branch": sourceBranch,
		"target_branch": targetBranch,
		"title":         title,
		"description":   description,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("vault scaffold: marshal merge request payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("vault scaffold: build merge request request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault scaffold: create merge request API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("vault scaffold: create merge request failed (HTTP %d): %s", resp.StatusCode, msg)
	}

	var mr struct {
		WebURL string `json:"web_url"`
	}
	if err := json.Unmarshal(respBody, &mr); err != nil {
		return "", fmt.Errorf("vault scaffold: parse merge request response: %w", err)
	}
	if strings.TrimSpace(mr.WebURL) == "" {
		return "", errors.New("vault scaffold: merge request created but web_url is empty")
	}
	return mr.WebURL, nil
}
