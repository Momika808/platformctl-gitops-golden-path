package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	ownershipManagedByKey = "platform.adminwg.dad/managed-by"
	ownershipAppKey       = "platform.adminwg.dad/app"
	ownershipLayerKey     = "platform.adminwg.dad/layer"
	ownershipManagedByVal = "platformctl"

	deleteStatusPlanned           = "planned"
	deleteStatusK8sPruneMRCreated = "k8s_prune_mr_created"
	deleteStatusK8sPruneCIPassed  = "k8s_prune_ci_passed"
	deleteStatusK8sPruneMerged    = "k8s_prune_merged"
	deleteStatusFluxPruneWaiting  = "flux_prune_waiting"
	deleteStatusFluxPruneDone     = "flux_prune_done"
	deleteStatusK8sCleanMRCreated = "k8s_cleanup_mr_created"
	deleteStatusK8sCleanCIPassed  = "k8s_cleanup_ci_passed"
	deleteStatusK8sCleanMerged    = "k8s_cleanup_merged"
	deleteStatusVaultMRCreated    = "vault_cleanup_mr_created"
	deleteStatusVaultCIPassed     = "vault_cleanup_ci_passed"
	deleteStatusVaultMerged       = "vault_cleanup_merged"
	deleteStatusDone              = "done"
	deleteStatusAborted           = "aborted"
)

var protectedNamespaces = map[string]struct{}{
	"kube-system":  {},
	"flux-system":  {},
	"vault":        {},
	"harbor":       {},
	"cicd":         {},
	"monitoring":   {},
	"rook-ceph":    {},
	"cert-manager": {},
	"ingress":      {},
	"llm":          {},
}

var deleteStatusOrder = map[string]int{
	deleteStatusPlanned:           10,
	deleteStatusK8sPruneMRCreated: 20,
	deleteStatusK8sPruneCIPassed:  30,
	deleteStatusK8sPruneMerged:    40,
	deleteStatusFluxPruneWaiting:  50,
	deleteStatusFluxPruneDone:     60,
	deleteStatusK8sCleanMRCreated: 70,
	deleteStatusK8sCleanCIPassed:  80,
	deleteStatusK8sCleanMerged:    90,
	deleteStatusVaultMRCreated:    100,
	deleteStatusVaultCIPassed:     110,
	deleteStatusVaultMerged:       120,
	deleteStatusDone:              130,
	deleteStatusAborted:           140,
}

type deleteAppConfig struct {
	Layer               string
	Namespace           string
	App                 string
	RepoRoot            string
	VaultRepoRoot       string
	Confirm             string
	DestroyData         bool
	ConfirmDestroyData  string
	AllowProtected      bool
	SkipRuntimeChecks   bool
	CreateMR            bool
	Auto                bool
	AutoMerge           bool
	AutoWaitCI          bool
	AutoCITimeout       time.Duration
	AutoCIPoll          time.Duration
	WaitFluxPrune       bool
	FluxPruneTimeout    time.Duration
	FluxPrunePoll       time.Duration
	K8sRemote           string
	K8sBaseBranch       string
	K8sProject          string
	K8sGitLabURL        string
	K8sGitLabToken      string
	VaultRemote         string
	VaultBaseBranch     string
	VaultProject        string
	VaultGitLabURL      string
	VaultGitLabToken    string
	RemoveSourceBranch  bool
	RequestID           string
	Resume              bool
	Abort               bool
	DiagnoseTerminating bool
	ForceFinalizers     bool
	ConfirmFinalizers   string
}

type deleteAppPlan struct {
	LayerDir                   string `json:"layer_dir"`
	LayerKustomization         string `json:"layer_kustomization"`
	NamespaceFile              string `json:"namespace_file"`
	K8sFluxLayerFile           string `json:"k8s_flux_layer_file"`
	K8sFluxRootKustomization   string `json:"k8s_flux_root_kustomization"`
	K8sFluxSystemKustomization string `json:"k8s_flux_system_kustomization"`
	VaultRoleFile              string `json:"vault_role_file"`
	VaultPolicyFile            string `json:"vault_policy_file"`
	VaultRoleName              string `json:"vault_role_name"`
	VaultPolicyName            string `json:"vault_policy_name"`
}

type deleteMRResult struct {
	Branch string
	MRURL  string
}

type deleteAppState struct {
	RequestID          string        `json:"request_id"`
	Status             string        `json:"status"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	Layer              string        `json:"layer"`
	Namespace          string        `json:"namespace"`
	App                string        `json:"app"`
	RepoRoot           string        `json:"repo_root"`
	VaultRepoRoot      string        `json:"vault_repo_root"`
	Plan               deleteAppPlan `json:"plan"`
	Auto               bool          `json:"auto"`
	AutoMerge          bool          `json:"auto_merge"`
	AutoWaitCI         bool          `json:"auto_wait_ci"`
	WaitFluxPrune      bool          `json:"wait_flux_prune"`
	RemoveSourceBranch bool          `json:"remove_source_branch"`
	K8s                struct {
		Phase1Branch string `json:"phase1_branch"`
		Phase1MRURL  string `json:"phase1_mr_url"`
		Phase2Branch string `json:"phase2_branch"`
		Phase2MRURL  string `json:"phase2_mr_url"`
	} `json:"k8s"`
	Vault struct {
		Branch string `json:"branch"`
		MRURL  string `json:"mr_url"`
	} `json:"vault"`
}

func runDeleteApp(args []string) error {
	fs := flag.NewFlagSet("delete-app", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	cfg := deleteAppConfig{}
	fs.StringVar(&cfg.Layer, "layer", "", "layer in format NN-name")
	fs.StringVar(&cfg.Namespace, "namespace", "", "application namespace")
	fs.StringVar(&cfg.App, "app", "", "application id (defaults to namespace)")
	fs.StringVar(&cfg.RepoRoot, "repo-root", "", "override repository root")
	fs.StringVar(&cfg.VaultRepoRoot, "vault-repo-root", "", "path to vault-control-plane repository (default ../vault-control-plane)")
	fs.StringVar(&cfg.Confirm, "confirm", "", "must equal --namespace for mutating operations")
	fs.BoolVar(&cfg.DestroyData, "destroy-data", false, "allow deletion when PVC exists in namespace")
	fs.StringVar(&cfg.ConfirmDestroyData, "confirm-destroy-data", "", "must equal --namespace when --destroy-data is set")
	fs.BoolVar(&cfg.AllowProtected, "allow-protected", false, "allow deletion of protected namespaces")
	fs.BoolVar(&cfg.SkipRuntimeChecks, "skip-runtime-checks", false, "skip kubectl runtime checks")
	fs.BoolVar(&cfg.CreateMR, "create-mr", false, "create phase-1 prune MR only")
	fs.BoolVar(&cfg.Auto, "auto", false, "create phase-1 MR and optionally continue automatically")
	fs.BoolVar(&cfg.AutoMerge, "auto-merge", false, "complete full two-phase delete and vault cleanup with merges")
	fs.BoolVar(&cfg.AutoWaitCI, "auto-wait-ci", true, "wait CI for each generated branch when --auto is enabled")
	fs.DurationVar(&cfg.AutoCITimeout, "auto-ci-timeout", 45*time.Minute, "CI wait timeout for --auto")
	fs.DurationVar(&cfg.AutoCIPoll, "auto-ci-poll-interval", 10*time.Second, "CI poll interval for --auto")
	fs.BoolVar(&cfg.WaitFluxPrune, "wait-flux-prune", true, "after phase-1 merge wait until namespace is deleted")
	fs.DurationVar(&cfg.FluxPruneTimeout, "flux-prune-timeout", 20*time.Minute, "namespace prune wait timeout")
	fs.DurationVar(&cfg.FluxPrunePoll, "flux-prune-poll-interval", 10*time.Second, "namespace prune poll interval")
	fs.StringVar(&cfg.K8sRemote, "k8s-remote", "origin", "git remote for k8s repository")
	fs.StringVar(&cfg.K8sBaseBranch, "k8s-base-branch", "master", "target/base branch for k8s merge requests")
	fs.StringVar(&cfg.K8sProject, "k8s-project", "cluster/k8s", "GitLab project id or path for k8s merge request API")
	fs.StringVar(&cfg.K8sGitLabURL, "k8s-gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", "https://example.internal"), "GitLab base URL for k8s merge request API")
	fs.StringVar(&cfg.K8sGitLabToken, "k8s-gitlab-token", os.Getenv("PLATFORM_GITLAB_TOKEN"), "GitLab token for k8s merge request")
	fs.StringVar(&cfg.VaultRemote, "vault-remote", "origin", "git remote for vault-control-plane repository")
	fs.StringVar(&cfg.VaultBaseBranch, "vault-base-branch", "main", "target/base branch for vault-control-plane merge requests")
	fs.StringVar(&cfg.VaultProject, "vault-project", "cluster/vault-control-plane", "GitLab project id or path for vault-control-plane merge request API")
	fs.StringVar(&cfg.VaultGitLabURL, "vault-gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", "https://example.internal"), "GitLab base URL for vault merge request API")
	fs.StringVar(&cfg.VaultGitLabToken, "vault-gitlab-token", os.Getenv("PLATFORM_GITLAB_TOKEN"), "GitLab token for vault merge request")
	fs.BoolVar(&cfg.RemoveSourceBranch, "auto-merge-remove-source-branch", true, "remove source branch after successful auto merge")
	fs.StringVar(&cfg.RequestID, "request-id", "", "state machine request id (required for --resume/--abort)")
	fs.BoolVar(&cfg.Resume, "resume", false, "resume delete workflow from saved state")
	fs.BoolVar(&cfg.Abort, "abort", false, "mark workflow request as aborted")
	fs.BoolVar(&cfg.DiagnoseTerminating, "diagnose-terminating", false, "diagnose namespace termination/finalizers")
	fs.BoolVar(&cfg.ForceFinalizers, "force-finalizers", false, "manual emergency namespace finalizers cleanup (never automatic)")
	fs.StringVar(&cfg.ConfirmFinalizers, "confirm-finalizers", "", "must equal --namespace when --force-finalizers is used")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if cfg.App == "" {
		cfg.App = cfg.Namespace
	}

	root, err := resolveRepoRoot(cfg.RepoRoot)
	if err != nil {
		return err
	}
	cfg.RepoRoot = root
	if cfg.VaultRepoRoot == "" {
		cfg.VaultRepoRoot = filepath.Join(filepath.Dir(root), "vault-control-plane")
	} else if !filepath.IsAbs(cfg.VaultRepoRoot) {
		cfg.VaultRepoRoot = filepath.Join(root, cfg.VaultRepoRoot)
	}

	if cfg.DiagnoseTerminating {
		if strings.TrimSpace(cfg.Namespace) == "" {
			return errors.New("--diagnose-terminating requires --namespace")
		}
		return diagnoseNamespaceTermination(cfg.Namespace)
	}

	if cfg.ForceFinalizers {
		if strings.TrimSpace(cfg.Namespace) == "" {
			return errors.New("--force-finalizers requires --namespace")
		}
		if cfg.ConfirmFinalizers != cfg.Namespace {
			return errors.New("--force-finalizers requires --confirm-finalizers=<namespace>")
		}
		return forceNamespaceFinalizers(cfg.Namespace)
	}

	if cfg.Resume && cfg.Abort {
		return errors.New("use one mode: --resume or --abort")
	}
	if cfg.Resume || cfg.Abort {
		if strings.TrimSpace(cfg.RequestID) == "" {
			return errors.New("--resume/--abort requires --request-id")
		}
		state, err := loadDeleteAppState(cfg.RepoRoot, cfg.RequestID)
		if err != nil {
			return err
		}
		if cfg.Abort {
			state.Status = deleteStatusAborted
			return saveDeleteAppState(cfg.RepoRoot, state)
		}
		if state.Status == deleteStatusAborted {
			return fmt.Errorf("request %s is aborted", state.RequestID)
		}
		cfg.Layer = state.Layer
		cfg.Namespace = state.Namespace
		cfg.App = state.App
		cfg.VaultRepoRoot = state.VaultRepoRoot
		cfg.Auto = state.Auto
		cfg.AutoMerge = state.AutoMerge
		cfg.AutoWaitCI = state.AutoWaitCI
		cfg.WaitFluxPrune = state.WaitFluxPrune
		cfg.RemoveSourceBranch = state.RemoveSourceBranch
		if cfg.Confirm != cfg.Namespace {
			return errors.New("--resume requires --confirm=<namespace>")
		}
		if strings.TrimSpace(cfg.K8sGitLabToken) == "" || strings.TrimSpace(cfg.VaultGitLabToken) == "" {
			return errors.New("--resume requires --k8s-gitlab-token and --vault-gitlab-token (or PLATFORM_GITLAB_TOKEN)")
		}
		return executeDeleteWorkflow(cfg, state.Plan, state)
	}

	if strings.TrimSpace(cfg.Layer) == "" || strings.TrimSpace(cfg.Namespace) == "" {
		return errors.New("required flags: --layer --namespace")
	}
	if !layerNameRe.MatchString(cfg.Layer) {
		return fmt.Errorf("invalid --layer: %s (expected NN-name)", cfg.Layer)
	}
	if !dns1123NameRe.MatchString(cfg.Namespace) {
		return fmt.Errorf("invalid --namespace: %s", cfg.Namespace)
	}
	if !dns1123NameRe.MatchString(cfg.App) {
		return fmt.Errorf("invalid --app: %s", cfg.App)
	}
	if _, protected := protectedNamespaces[cfg.Namespace]; protected && !cfg.AllowProtected {
		return fmt.Errorf("namespace %q is protected; refuse delete without --allow-protected", cfg.Namespace)
	}
	if cfg.DestroyData && strings.TrimSpace(cfg.ConfirmDestroyData) != cfg.Namespace {
		return errors.New("--destroy-data requires --confirm-destroy-data=<namespace>")
	}
	if cfg.AutoMerge && !cfg.Auto {
		return errors.New("--auto-merge requires --auto")
	}
	if cfg.Auto && !cfg.CreateMR {
		cfg.CreateMR = true
	}
	if cfg.AutoMerge && !cfg.AutoWaitCI {
		return errors.New("--auto-merge requires --auto-wait-ci=true")
	}
	if cfg.AutoCITimeout <= 0 || cfg.AutoCIPoll <= 0 {
		return errors.New("auto CI timeout and poll interval must be > 0")
	}
	if cfg.FluxPruneTimeout <= 0 || cfg.FluxPrunePoll <= 0 {
		return errors.New("flux prune timeout and poll interval must be > 0")
	}
	if cfg.CreateMR || cfg.Auto {
		if strings.TrimSpace(cfg.Confirm) != cfg.Namespace {
			return errors.New("mutating delete requires --confirm=<namespace>")
		}
		if strings.TrimSpace(cfg.K8sGitLabToken) == "" || strings.TrimSpace(cfg.VaultGitLabToken) == "" {
			return errors.New("mutating delete requires --k8s-gitlab-token and --vault-gitlab-token (or PLATFORM_GITLAB_TOKEN)")
		}
	}

	plan, err := buildDeleteAppPlan(cfg)
	if err != nil {
		return err
	}
	if err := preflightDeleteApp(cfg, plan); err != nil {
		return err
	}
	printDeletePlan(cfg, plan)

	if !cfg.CreateMR {
		return nil
	}

	if cfg.RequestID == "" {
		cfg.RequestID = fmt.Sprintf("delete-%s-%s", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	}
	state := &deleteAppState{
		RequestID:          cfg.RequestID,
		Status:             deleteStatusPlanned,
		CreatedAt:          time.Now().UTC(),
		UpdatedAt:          time.Now().UTC(),
		Layer:              cfg.Layer,
		Namespace:          cfg.Namespace,
		App:                cfg.App,
		RepoRoot:           cfg.RepoRoot,
		VaultRepoRoot:      cfg.VaultRepoRoot,
		Plan:               plan,
		Auto:               cfg.Auto,
		AutoMerge:          cfg.AutoMerge,
		AutoWaitCI:         cfg.AutoWaitCI,
		WaitFluxPrune:      cfg.WaitFluxPrune,
		RemoveSourceBranch: cfg.RemoveSourceBranch,
	}
	if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
		return err
	}
	fmt.Printf("Request ID: %s\n", cfg.RequestID)
	return executeDeleteWorkflow(cfg, plan, state)
}

func executeDeleteWorkflow(cfg deleteAppConfig, plan deleteAppPlan, state *deleteAppState) error {
	if state.Status == deleteStatusAborted {
		return fmt.Errorf("request %s is aborted", state.RequestID)
	}
	startedAt := time.Now().UTC()
	logInfo("delete_app_workflow_start", map[string]any{
		"component":  "delete-app",
		"request_id": state.RequestID,
		"app":        cfg.App,
		"namespace":  cfg.Namespace,
		"layer":      cfg.Layer,
		"auto":       cfg.Auto,
		"auto_merge": cfg.AutoMerge,
	})

	var (
		k8sClient   *gitLabClient
		vaultClient *gitLabClient
		err         error
	)
	if cfg.Auto || cfg.AutoMerge {
		k8sClient, err = newGitLabClient(cfg.K8sGitLabURL, cfg.K8sGitLabToken, 15*time.Second)
		if err != nil {
			return fmt.Errorf("init k8s gitlab client: %w", err)
		}
		vaultClient, err = newGitLabClient(cfg.VaultGitLabURL, cfg.VaultGitLabToken, 15*time.Second)
		if err != nil {
			return fmt.Errorf("init vault gitlab client: %w", err)
		}
	}

	if !statusReached(state.Status, deleteStatusK8sPruneMRCreated) {
		phase1, err := createDeletePhase1K8sMR(cfg, plan)
		if err != nil {
			logError("delete_app_phase1_mr_create_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"error":      err.Error(),
			})
			emitAlertIfConfigured("platformctl delete-app phase1 MR failed", map[string]any{
				"request_id": state.RequestID,
				"layer":      cfg.Layer,
				"namespace":  cfg.Namespace,
				"error":      err.Error(),
			})
			return err
		}
		state.K8s.Phase1Branch = phase1.Branch
		state.K8s.Phase1MRURL = phase1.MRURL
		state.Status = deleteStatusK8sPruneMRCreated
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
		fmt.Printf("Created phase-1 k8s prune MR: %s\n", phase1.MRURL)
		logInfo("delete_app_phase1_mr_created", map[string]any{
			"component":  "delete-app",
			"request_id": state.RequestID,
			"mr_url":     phase1.MRURL,
			"branch":     phase1.Branch,
		})
	}

	if !cfg.Auto {
		fmt.Println("Phase-1 MR created. Stop (safe mode).")
		return nil
	}

	if cfg.AutoWaitCI && !statusReached(state.Status, deleteStatusK8sPruneCIPassed) {
		fmt.Println("Waiting k8s CI (phase-1 prune)...")
		pipe, err := waitForBranchPipelineSuccess(k8sClient, cfg.K8sProject, state.K8s.Phase1Branch, cfg.AutoCITimeout, cfg.AutoCIPoll)
		if err != nil {
			logError("delete_app_phase1_ci_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.K8sProject,
				"branch":     state.K8s.Phase1Branch,
				"error":      err.Error(),
			})
			emitAlertIfConfigured("platformctl delete-app phase1 CI failed", map[string]any{
				"request_id": state.RequestID,
				"project":    cfg.K8sProject,
				"branch":     state.K8s.Phase1Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("k8s phase-1 CI failed: %w", err)
		}
		fmt.Printf("k8s phase-1 pipeline #%d passed: %s\n", pipe.ID, pipe.WebURL)
		state.Status = deleteStatusK8sPruneCIPassed
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	if !cfg.AutoMerge {
		fmt.Println("Auto mode without merge completed after phase-1.")
		return nil
	}

	if !statusReached(state.Status, deleteStatusK8sPruneMerged) {
		fmt.Println("Merging k8s phase-1 prune MR...")
		if _, err := mergeMergeRequestBySourceBranch(k8sClient, cfg.K8sProject, state.K8s.Phase1Branch, cfg.RemoveSourceBranch); err != nil {
			logError("delete_app_phase1_merge_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.K8sProject,
				"branch":     state.K8s.Phase1Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("merge k8s phase-1: %w", err)
		}
		state.Status = deleteStatusK8sPruneMerged
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	if cfg.WaitFluxPrune {
		if cfg.SkipRuntimeChecks {
			return errors.New("--wait-flux-prune=true requires runtime checks (disable --skip-runtime-checks)")
		}
		if !statusReached(state.Status, deleteStatusFluxPruneWaiting) {
			state.Status = deleteStatusFluxPruneWaiting
			if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
				return err
			}
		}
		if !statusReached(state.Status, deleteStatusFluxPruneDone) {
			fmt.Println("Waiting namespace prune...")
			if err := waitNamespaceDeleted(cfg.Namespace, cfg.FluxPruneTimeout, cfg.FluxPrunePoll); err != nil {
				logError("delete_app_flux_prune_failed", map[string]any{
					"component":  "delete-app",
					"request_id": state.RequestID,
					"namespace":  cfg.Namespace,
					"error":      err.Error(),
				})
				emitAlertIfConfigured("platformctl delete-app flux prune failed", map[string]any{
					"request_id": state.RequestID,
					"namespace":  cfg.Namespace,
					"error":      err.Error(),
				})
				return err
			}
			fmt.Println("Namespace prune completed.")
			state.Status = deleteStatusFluxPruneDone
			if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
				return err
			}
		}
	}

	if !statusReached(state.Status, deleteStatusK8sCleanMRCreated) {
		phase2, err := createDeletePhase2K8sMR(cfg, plan)
		if err != nil {
			logError("delete_app_phase2_mr_create_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"error":      err.Error(),
			})
			return err
		}
		state.K8s.Phase2Branch = phase2.Branch
		state.K8s.Phase2MRURL = phase2.MRURL
		state.Status = deleteStatusK8sCleanMRCreated
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
		fmt.Printf("Created phase-2 k8s cleanup MR: %s\n", phase2.MRURL)
		logInfo("delete_app_phase2_mr_created", map[string]any{
			"component":  "delete-app",
			"request_id": state.RequestID,
			"mr_url":     phase2.MRURL,
			"branch":     phase2.Branch,
		})
	}

	if cfg.AutoWaitCI && !statusReached(state.Status, deleteStatusK8sCleanCIPassed) {
		fmt.Println("Waiting k8s CI (phase-2 cleanup)...")
		pipe, err := waitForBranchPipelineSuccess(k8sClient, cfg.K8sProject, state.K8s.Phase2Branch, cfg.AutoCITimeout, cfg.AutoCIPoll)
		if err != nil {
			logError("delete_app_phase2_ci_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.K8sProject,
				"branch":     state.K8s.Phase2Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("k8s phase-2 CI failed: %w", err)
		}
		fmt.Printf("k8s phase-2 pipeline #%d passed: %s\n", pipe.ID, pipe.WebURL)
		state.Status = deleteStatusK8sCleanCIPassed
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	if !statusReached(state.Status, deleteStatusK8sCleanMerged) {
		fmt.Println("Merging k8s phase-2 cleanup MR...")
		if _, err := mergeMergeRequestBySourceBranch(k8sClient, cfg.K8sProject, state.K8s.Phase2Branch, cfg.RemoveSourceBranch); err != nil {
			logError("delete_app_phase2_merge_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.K8sProject,
				"branch":     state.K8s.Phase2Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("merge k8s phase-2: %w", err)
		}
		state.Status = deleteStatusK8sCleanMerged
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	if !statusReached(state.Status, deleteStatusVaultMRCreated) {
		vaultPhase, err := createDeleteVaultCleanupMR(cfg, plan)
		if err != nil {
			logError("delete_app_vault_mr_create_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"error":      err.Error(),
			})
			return err
		}
		state.Vault.Branch = vaultPhase.Branch
		state.Vault.MRURL = vaultPhase.MRURL
		state.Status = deleteStatusVaultMRCreated
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
		fmt.Printf("Created vault cleanup MR: %s\n", vaultPhase.MRURL)
		logInfo("delete_app_vault_mr_created", map[string]any{
			"component":  "delete-app",
			"request_id": state.RequestID,
			"mr_url":     vaultPhase.MRURL,
			"branch":     vaultPhase.Branch,
		})
	}

	if cfg.AutoWaitCI && !statusReached(state.Status, deleteStatusVaultCIPassed) {
		fmt.Println("Waiting vault-control-plane CI...")
		pipe, err := waitForBranchPipelineSuccess(vaultClient, cfg.VaultProject, state.Vault.Branch, cfg.AutoCITimeout, cfg.AutoCIPoll)
		if err != nil {
			logError("delete_app_vault_ci_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.VaultProject,
				"branch":     state.Vault.Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("vault CI failed: %w", err)
		}
		fmt.Printf("vault pipeline #%d passed: %s\n", pipe.ID, pipe.WebURL)
		state.Status = deleteStatusVaultCIPassed
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	if !statusReached(state.Status, deleteStatusVaultMerged) {
		fmt.Println("Merging vault cleanup MR...")
		if _, err := mergeMergeRequestBySourceBranch(vaultClient, cfg.VaultProject, state.Vault.Branch, cfg.RemoveSourceBranch); err != nil {
			logError("delete_app_vault_merge_failed", map[string]any{
				"component":  "delete-app",
				"request_id": state.RequestID,
				"project":    cfg.VaultProject,
				"branch":     state.Vault.Branch,
				"error":      err.Error(),
			})
			return fmt.Errorf("merge vault cleanup: %w", err)
		}
		state.Status = deleteStatusVaultMerged
		if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
			return err
		}
	}

	state.Status = deleteStatusDone
	if err := saveDeleteAppState(cfg.RepoRoot, state); err != nil {
		return err
	}
	fmt.Println("Delete-app auto merge flow completed.")
	logInfo("delete_app_workflow_completed", map[string]any{
		"component":  "delete-app",
		"request_id": state.RequestID,
		"namespace":  cfg.Namespace,
		"layer":      cfg.Layer,
		"elapsed_ms": time.Since(startedAt).Milliseconds(),
	})
	return nil
}

func statusReached(current, target string) bool {
	return deleteStatusOrder[current] >= deleteStatusOrder[target]
}

func buildDeleteAppPlan(cfg deleteAppConfig) (deleteAppPlan, error) {
	plan := deleteAppPlan{
		LayerDir:                   filepath.Join(cfg.RepoRoot, "clusters", "homelab", cfg.Layer),
		LayerKustomization:         filepath.Join(cfg.RepoRoot, "clusters", "homelab", cfg.Layer, "kustomization.yaml"),
		NamespaceFile:              filepath.Join(cfg.RepoRoot, "clusters", "homelab", cfg.Layer, "namespace.yaml"),
		K8sFluxLayerFile:           filepath.Join(cfg.RepoRoot, "clusters", "homelab", "flux-system", "ks-"+cfg.Layer+".yaml"),
		K8sFluxRootKustomization:   filepath.Join(cfg.RepoRoot, "clusters", "homelab", "00-flux-ks", "kustomization.yaml"),
		K8sFluxSystemKustomization: filepath.Join(cfg.RepoRoot, "clusters", "homelab", "flux-system", "kustomization.yaml"),
		VaultRoleName:              "vso-" + cfg.Namespace,
		VaultPolicyName:            "vso-" + cfg.Namespace,
		VaultRoleFile:              filepath.Join(cfg.VaultRepoRoot, "roles.d", "vso-"+cfg.Namespace+".yaml"),
		VaultPolicyFile:            filepath.Join(cfg.VaultRepoRoot, "policies", "vso-"+cfg.Namespace+".hcl"),
	}

	if _, err := os.Stat(plan.LayerDir); err != nil {
		return plan, fmt.Errorf("layer not found: %s", relPath(cfg.RepoRoot, plan.LayerDir))
	}
	if _, err := os.Stat(plan.LayerKustomization); err != nil {
		return plan, fmt.Errorf("layer kustomization missing: %s", relPath(cfg.RepoRoot, plan.LayerKustomization))
	}
	if _, err := os.Stat(plan.NamespaceFile); err != nil {
		return plan, fmt.Errorf("namespace file missing: %s", relPath(cfg.RepoRoot, plan.NamespaceFile))
	}
	if _, err := os.Stat(plan.K8sFluxLayerFile); err != nil {
		return plan, fmt.Errorf("flux layer file missing: %s", relPath(cfg.RepoRoot, plan.K8sFluxLayerFile))
	}
	for _, path := range []string{plan.K8sFluxRootKustomization, plan.K8sFluxSystemKustomization} {
		if _, err := os.Stat(path); err != nil {
			return plan, fmt.Errorf("required kustomization missing: %s", relPath(cfg.RepoRoot, path))
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.VaultRepoRoot, ".git")); err != nil {
		return plan, fmt.Errorf("vault repo not found or not a git repository: %s", cfg.VaultRepoRoot)
	}

	return plan, nil
}

func preflightDeleteApp(cfg deleteAppConfig, plan deleteAppPlan) error {
	if err := verifyOwnershipLabelsInLayer(cfg, plan); err != nil {
		return err
	}

	if cfg.SkipRuntimeChecks {
		return nil
	}

	if err := verifyOwnershipLabelsRuntime(cfg); err != nil {
		return err
	}

	pvcs, err := getNamespacePVCs(cfg.Namespace)
	if err != nil {
		return fmt.Errorf("runtime preflight PVC check failed: %w", err)
	}
	if len(pvcs) > 0 && !cfg.DestroyData {
		return fmt.Errorf("namespace %s has PVCs (%s); rerun with --destroy-data --confirm-destroy-data=%s if intentional", cfg.Namespace, strings.Join(pvcs, ", "), cfg.Namespace)
	}
	return nil
}

func verifyOwnershipLabelsInLayer(cfg deleteAppConfig, plan deleteAppPlan) error {
	type nsDoc struct {
		Metadata struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	var doc nsDoc
	data, err := os.ReadFile(plan.NamespaceFile)
	if err != nil {
		return fmt.Errorf("read namespace file: %w", err)
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse namespace file: %w", err)
	}
	if doc.Metadata.Name != cfg.Namespace {
		return fmt.Errorf("ownership check failed: namespace file name=%q expected=%q", doc.Metadata.Name, cfg.Namespace)
	}
	if err := verifyOwnershipLabels(doc.Metadata.Labels, cfg); err != nil {
		return fmt.Errorf("ownership check failed in namespace file: %w", err)
	}
	return nil
}

func verifyOwnershipLabelsRuntime(cfg deleteAppConfig) error {
	labels, exists, err := getRuntimeNamespaceLabels(cfg.Namespace)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err := verifyOwnershipLabels(labels, cfg); err != nil {
		return fmt.Errorf("runtime namespace labels mismatch: %w", err)
	}
	return nil
}

func verifyOwnershipLabels(labels map[string]string, cfg deleteAppConfig) error {
	if labels == nil {
		return errors.New("labels are empty")
	}
	required := map[string]string{
		ownershipManagedByKey: ownershipManagedByVal,
		ownershipAppKey:       cfg.App,
		ownershipLayerKey:     cfg.Layer,
	}
	for key, expected := range required {
		got := strings.TrimSpace(labels[key])
		if got != expected {
			return fmt.Errorf("%s=%q expected=%q", key, got, expected)
		}
	}
	return nil
}

func printDeletePlan(cfg deleteAppConfig, plan deleteAppPlan) {
	fmt.Println("Delete plan:")
	fmt.Printf("  App: %s\n", cfg.App)
	fmt.Printf("  Namespace: %s\n", cfg.Namespace)
	fmt.Printf("  Layer: %s\n", cfg.Layer)
	fmt.Println("  K8s phase-1 (prune resources, keep Flux Kustomization):")
	fmt.Printf("    - rewrite resources in %s to empty\n", relPath(cfg.RepoRoot, plan.LayerKustomization))
	fmt.Println("  K8s phase-2 (cleanup layer):")
	fmt.Printf("    - remove %s\n", relPath(cfg.RepoRoot, plan.LayerDir))
	fmt.Printf("    - remove %s\n", relPath(cfg.RepoRoot, plan.K8sFluxLayerFile))
	fmt.Printf("    - remove reference in %s\n", relPath(cfg.RepoRoot, plan.K8sFluxRootKustomization))
	fmt.Printf("    - remove reference in %s\n", relPath(cfg.RepoRoot, plan.K8sFluxSystemKustomization))
	fmt.Println("  Vault cleanup:")
	fmt.Printf("    - remove %s\n", filepath.ToSlash(plan.VaultRoleFile))
	fmt.Printf("    - remove %s\n", filepath.ToSlash(plan.VaultPolicyFile))
	fmt.Println()
}

func createDeletePhase1K8sMR(cfg deleteAppConfig, plan deleteAppPlan) (deleteMRResult, error) {
	branch := fmt.Sprintf("platformctl/delete-app-%s-%s-k8s-prune", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	msg := fmt.Sprintf("k8s: prune app resources for %s", cfg.Namespace)
	title := fmt.Sprintf("k8s: phase-1 prune app %s", cfg.Namespace)
	description := fmt.Sprintf("Phase 1 delete-app for `%s`: prune resources, keep Flux Kustomization for controlled GC.", cfg.Namespace)

	modify := func() error {
		return setKustomizationResources(plan.LayerKustomization, []string{})
	}
	return createRepoMR(cfg.RepoRoot, cfg.K8sRemote, cfg.K8sBaseBranch, branch, msg, cfg.K8sGitLabURL, cfg.K8sGitLabToken, cfg.K8sProject, title, description, modify, []string{plan.LayerKustomization})
}

func createDeletePhase2K8sMR(cfg deleteAppConfig, plan deleteAppPlan) (deleteMRResult, error) {
	branch := fmt.Sprintf("platformctl/delete-app-%s-%s-k8s-cleanup", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	msg := fmt.Sprintf("k8s: remove app layer %s (%s)", cfg.Layer, cfg.Namespace)
	title := fmt.Sprintf("k8s: phase-2 cleanup app %s", cfg.Namespace)
	description := fmt.Sprintf("Phase 2 delete-app for `%s`: remove layer dir and Flux Kustomization wiring.", cfg.Namespace)

	modify := func() error {
		if err := removeResourceInKustomization(plan.K8sFluxRootKustomization, "../flux-system/ks-"+cfg.Layer+".yaml"); err != nil {
			return err
		}
		if err := removeResourceInKustomization(plan.K8sFluxSystemKustomization, "ks-"+cfg.Layer+".yaml"); err != nil {
			return err
		}
		if err := os.RemoveAll(plan.LayerDir); err != nil {
			return err
		}
		if err := os.Remove(plan.K8sFluxLayerFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	paths := []string{
		plan.K8sFluxRootKustomization,
		plan.K8sFluxSystemKustomization,
		plan.K8sFluxLayerFile,
		plan.LayerDir,
	}
	return createRepoMR(cfg.RepoRoot, cfg.K8sRemote, cfg.K8sBaseBranch, branch, msg, cfg.K8sGitLabURL, cfg.K8sGitLabToken, cfg.K8sProject, title, description, modify, paths)
}

func createDeleteVaultCleanupMR(cfg deleteAppConfig, plan deleteAppPlan) (deleteMRResult, error) {
	branch := fmt.Sprintf("platformctl/delete-app-%s-%s-vault-cleanup", cfg.Namespace, time.Now().UTC().Format("20060102150405"))
	msg := fmt.Sprintf("vault: remove role/policy for %s", cfg.Namespace)
	title := fmt.Sprintf("vault: cleanup app %s", cfg.Namespace)
	description := fmt.Sprintf("Cleanup Vault role/policy for deleted app `%s`.", cfg.Namespace)

	modify := func() error {
		if err := os.Remove(plan.VaultRoleFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(plan.VaultPolicyFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	paths := []string{plan.VaultRoleFile, plan.VaultPolicyFile}
	return createRepoMR(cfg.VaultRepoRoot, cfg.VaultRemote, cfg.VaultBaseBranch, branch, msg, cfg.VaultGitLabURL, cfg.VaultGitLabToken, cfg.VaultProject, title, description, modify, paths)
}

func createRepoMR(repoRoot, remote, baseBranch, branch, commitMessage, gitlabURL, gitlabToken, gitlabProject, title, description string, modify func() error, stagePaths []string) (deleteMRResult, error) {
	var result deleteMRResult
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	originalBranch, _ := gitCurrentBranch(repoRoot)
	if err := runGit(repoRoot, "fetch", remote, baseBranch); err != nil {
		return result, fmt.Errorf("git fetch failed: %w", err)
	}
	if err := runGit(repoRoot, "checkout", "-B", branch, remote+"/"+baseBranch); err != nil {
		return result, fmt.Errorf("git checkout failed: %w", err)
	}
	defer func() {
		if originalBranch != "" && originalBranch != "HEAD" && originalBranch != branch {
			_ = runGit(repoRoot, "checkout", originalBranch)
		}
	}()

	if err := modify(); err != nil {
		return result, err
	}

	for _, path := range stagePaths {
		if err := stagePath(repoRoot, path); err != nil {
			return result, err
		}
	}

	dirty, err := gitHasStagedChanges(repoRoot)
	if err != nil {
		return result, err
	}
	if !dirty {
		return result, errors.New("no staged changes to commit")
	}
	if err := runGit(repoRoot, "commit", "-m", commitMessage); err != nil {
		return result, err
	}
	if err := runGit(repoRoot, "push", "-u", remote, branch); err != nil {
		return result, err
	}

	mrURL, err := createGitLabMR(gitlabURL, gitlabToken, gitlabProject, branch, baseBranch, title, description)
	if err != nil {
		return result, err
	}
	result.Branch = branch
	result.MRURL = mrURL
	return result, nil
}

func stagePath(repoRoot, path string) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return runGit(repoRoot, "add", "-A")
	}
	_, statErr := os.Stat(path)
	if statErr == nil {
		return runGit(repoRoot, "add", rel)
	}
	if errors.Is(statErr, os.ErrNotExist) {
		return runGit(repoRoot, "add", "-A", rel)
	}
	return statErr
}

func setKustomizationResources(path string, resources []string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse kustomization %s: %w", path, err)
	}

	resourceAny := make([]any, 0, len(resources))
	for _, item := range resources {
		resourceAny = append(resourceAny, item)
	}
	doc["resources"] = resourceAny

	content, err := marshalYAMLWithIndent(doc, 2)
	if err != nil {
		return fmt.Errorf("marshal kustomization %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write kustomization %s: %w", path, err)
	}
	return nil
}

func removeResourceInKustomization(path, resource string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse kustomization %s: %w", path, err)
	}

	var kept []string
	if raw, ok := doc["resources"]; ok {
		switch typed := raw.(type) {
		case []any:
			for _, item := range typed {
				s, ok := item.(string)
				if !ok || strings.TrimSpace(s) == "" {
					continue
				}
				if s != resource {
					kept = append(kept, s)
				}
			}
		case []string:
			for _, s := range typed {
				if s != resource {
					kept = append(kept, s)
				}
			}
		}
	}

	resourceAny := make([]any, 0, len(kept))
	for _, item := range kept {
		resourceAny = append(resourceAny, item)
	}
	doc["resources"] = resourceAny

	content, err := marshalYAMLWithIndent(doc, 2)
	if err != nil {
		return fmt.Errorf("marshal kustomization %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write kustomization %s: %w", path, err)
	}
	return nil
}

func getNamespacePVCs(namespace string) ([]string, error) {
	cmd := exec.Command("kubectl", "-n", namespace, "get", "pvc", "-o", "name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		low := strings.ToLower(msg)
		if strings.Contains(low, "not found") || strings.Contains(low, "namespaces \"") {
			return nil, nil
		}
		return nil, fmt.Errorf("kubectl get pvc failed: %s", msg)
	}
	var result []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, line)
	}
	return result, nil
}

func waitNamespaceDeleted(namespace string, timeout, poll time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting namespace %s deletion", namespace)
		}

		cmd := exec.Command("kubectl", "get", "namespace", namespace, "-o", "name")
		out, err := cmd.CombinedOutput()
		if err != nil {
			msg := strings.ToLower(strings.TrimSpace(string(out)))
			if strings.Contains(msg, "notfound") || strings.Contains(msg, "not found") {
				return nil
			}
			return fmt.Errorf("kubectl get namespace failed: %s", strings.TrimSpace(string(out)))
		}
		fmt.Printf("  namespace still present: %s\n", strings.TrimSpace(string(out)))
		time.Sleep(poll)
	}
}

func diagnoseNamespaceTermination(namespace string) error {
	type nsDoc struct {
		Metadata struct {
			Name       string            `yaml:"name"`
			Labels     map[string]string `yaml:"labels"`
			Finalizers []string          `yaml:"finalizers"`
		} `yaml:"metadata"`
		Status struct {
			Phase string `yaml:"phase"`
		} `yaml:"status"`
	}

	cmd := exec.Command("kubectl", "get", "namespace", namespace, "-o", "yaml")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(msg, "notfound") || strings.Contains(msg, "not found") {
			fmt.Printf("Namespace %s not found.\n", namespace)
			return nil
		}
		return fmt.Errorf("kubectl get namespace failed: %s", strings.TrimSpace(string(out)))
	}

	var doc nsDoc
	if err := yaml.Unmarshal(out, &doc); err != nil {
		return fmt.Errorf("parse namespace yaml: %w", err)
	}
	fmt.Printf("Namespace: %s\n", doc.Metadata.Name)
	fmt.Printf("Phase: %s\n", doc.Status.Phase)
	fmt.Printf("Finalizers: %v\n", doc.Metadata.Finalizers)
	fmt.Printf("Ownership labels: managed-by=%q app=%q layer=%q\n",
		doc.Metadata.Labels[ownershipManagedByKey],
		doc.Metadata.Labels[ownershipAppKey],
		doc.Metadata.Labels[ownershipLayerKey],
	)

	kindsCmd := exec.Command("kubectl", "api-resources", "--verbs=list", "--namespaced", "-o", "name")
	kindsOut, err := kindsCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl api-resources failed: %s", strings.TrimSpace(string(kindsOut)))
	}
	var leftovers []string
	for _, kind := range strings.Split(string(kindsOut), "\n") {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		getCmd := exec.Command("kubectl", "-n", namespace, "get", kind, "--ignore-not-found", "-o", "name")
		resOut, getErr := getCmd.CombinedOutput()
		if getErr != nil {
			continue
		}
		for _, line := range strings.Split(string(resOut), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				leftovers = append(leftovers, line)
			}
		}
	}

	if len(leftovers) == 0 {
		fmt.Println("Remaining namespaced resources: none")
		return nil
	}
	fmt.Println("Remaining namespaced resources:")
	for _, item := range leftovers {
		fmt.Printf("  - %s\n", item)
	}
	return nil
}

func forceNamespaceFinalizers(namespace string) error {
	patch := `{"metadata":{"finalizers":[]}}`
	cmd := exec.Command("kubectl", "patch", "namespace", namespace, "--type=merge", "-p", patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl patch namespace finalizers failed: %s", strings.TrimSpace(string(out)))
	}
	fmt.Printf("Namespace finalizers patch applied for %s.\n", namespace)
	return nil
}

func getRuntimeNamespaceLabels(namespace string) (map[string]string, bool, error) {
	type nsDoc struct {
		Metadata struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	cmd := exec.Command("kubectl", "get", "namespace", namespace, "-o", "yaml")
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(out)))
		if strings.Contains(msg, "notfound") || strings.Contains(msg, "not found") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("kubectl get namespace failed: %s", strings.TrimSpace(string(out)))
	}
	var doc nsDoc
	if err := yaml.Unmarshal(out, &doc); err != nil {
		return nil, false, fmt.Errorf("parse runtime namespace yaml: %w", err)
	}
	return doc.Metadata.Labels, true, nil
}

func deleteAppStatePath(repoRoot, requestID string) string {
	return filepath.Join(repoRoot, ".platformctl-state", "delete-app", requestID+".json")
}

func saveDeleteAppState(repoRoot string, st *deleteAppState) error {
	if strings.TrimSpace(st.RequestID) == "" {
		return errors.New("state request id is empty")
	}
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	st.UpdatedAt = time.Now().UTC()
	path := deleteAppStatePath(repoRoot, st.RequestID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func loadDeleteAppState(repoRoot, requestID string) (*deleteAppState, error) {
	path := deleteAppStatePath(repoRoot, requestID)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st deleteAppState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if st.RequestID == "" {
		return nil, fmt.Errorf("invalid state file: missing request_id (%s)", path)
	}
	return &st, nil
}
