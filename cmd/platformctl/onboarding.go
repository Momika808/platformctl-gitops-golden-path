package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
	"gopkg.in/yaml.v3"
)

var (
	dns1123NameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	layerNameRe   = regexp.MustCompile(`^[0-9]{2}-[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

func runNewService(args []string) error {
	fsNewService := flag.NewFlagSet("new-service", flag.ContinueOnError)
	fsNewService.SetOutput(os.Stderr)

	var (
		layer                   string
		namespace               string
		name                    string
		image                   string
		tag                     string
		port                    int
		resourceProfile         string
		repoRoot                string
		withImageAutomation     bool
		noImageAutomation       bool
		fluxGitSource           string
		fluxGitNamespace        string
		imagePolicyTagPattern   string
		imagePolicyOrder        string
		imageAutomationInterval string
		imageRepositoryInterval string
		imagePolicyInterval     string
		harborCASecret          string
		registrySecret          string
	)

	fsNewService.StringVar(&layer, "layer", "", "layer in format NN-name (example: 11-rag)")
	fsNewService.StringVar(&namespace, "namespace", "", "namespace name")
	fsNewService.StringVar(&name, "name", "", "service/app name")
	fsNewService.StringVar(&image, "image", "", "container image repository")
	fsNewService.StringVar(&tag, "tag", "", "container image tag")
	fsNewService.IntVar(&port, "port", 0, "service/container port")
	fsNewService.StringVar(&resourceProfile, "resource-profile", "tiny", "resource profile: tiny|small|medium|large")
	fsNewService.StringVar(&repoRoot, "repo-root", "", "override repository root")

	fsNewService.BoolVar(&withImageAutomation, "with-image-automation", true, "enable image automation")
	fsNewService.BoolVar(&noImageAutomation, "no-image-automation", false, "disable image automation")
	fsNewService.StringVar(&fluxGitSource, "flux-git-source", "homelab-write", "Flux GitRepository source for image automation")
	fsNewService.StringVar(&fluxGitNamespace, "flux-git-namespace", "flux-system", "namespace of Flux GitRepository source")
	fsNewService.StringVar(&imagePolicyTagPattern, "image-policy-tag-pattern", "^main$", "ImagePolicy filterTags pattern")
	fsNewService.StringVar(&imagePolicyOrder, "image-policy-order", "asc", "ImagePolicy alphabetical order: asc|desc")
	fsNewService.StringVar(&imageAutomationInterval, "image-automation-interval", "5m", "ImageUpdateAutomation interval")
	fsNewService.StringVar(&imageRepositoryInterval, "image-repository-interval", "5m", "ImageRepository interval")
	fsNewService.StringVar(&imagePolicyInterval, "image-policy-interval", "5m", "ImagePolicy interval")
	fsNewService.StringVar(&harborCASecret, "harbor-ca-secret", "harbor-oci-ca", "Secret name with Harbor CA")
	fsNewService.StringVar(&registrySecret, "registry-secret", "harbor-proxy-robot", "Registry pull secret name")

	if err := fsNewService.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if noImageAutomation {
		withImageAutomation = false
	}

	if strings.TrimSpace(layer) == "" || strings.TrimSpace(namespace) == "" || strings.TrimSpace(name) == "" ||
		strings.TrimSpace(image) == "" || strings.TrimSpace(tag) == "" || port <= 0 {
		return errors.New("required flags: --layer --namespace --name --image --tag --port")
	}
	if !layerNameRe.MatchString(layer) {
		return fmt.Errorf("invalid --layer: %s (expected NN-name)", layer)
	}
	if !dns1123NameRe.MatchString(namespace) {
		return fmt.Errorf("invalid --namespace: %s", namespace)
	}
	if !dns1123NameRe.MatchString(name) {
		return fmt.Errorf("invalid --name: %s", name)
	}
	if imagePolicyOrder != "asc" && imagePolicyOrder != "desc" {
		return errors.New("--image-policy-order must be asc or desc")
	}
	if resourceProfile != "tiny" && resourceProfile != "small" && resourceProfile != "medium" && resourceProfile != "large" {
		return errors.New("--resource-profile must be one of: tiny, small, medium, large")
	}
	if port < 1 || port > 65535 {
		return errors.New("--port must be in range 1..65535")
	}

	root, err := resolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}

	layerDir := filepath.Join(root, "clusters", "homelab", layer)
	layerKustomization := filepath.Join(layerDir, "kustomization.yaml")
	appDir := filepath.Join(layerDir, "apps", name)
	appFile := filepath.Join(appDir, "app.yaml")

	if _, err := os.Stat(layerDir); err != nil {
		return fmt.Errorf("layer not found: %s", relPath(root, layerDir))
	}
	if _, err := os.Stat(layerKustomization); err != nil {
		return fmt.Errorf("layer kustomization not found: %s", relPath(root, layerKustomization))
	}
	if _, err := os.Stat(appDir); err == nil {
		return fmt.Errorf("service already exists: %s", relPath(root, appDir))
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return fmt.Errorf("create app dir: %w", err)
	}

	spec := &appspec.ServiceApp{
		APIVersion: "platform.adminwg.dad/v1alpha1",
		Kind:       "ServiceApp",
		Metadata:   appspec.Metadata{Name: name},
		Spec: appspec.Spec{
			Layer:         layer,
			Namespace:     namespace,
			Tier:          "dev",
			ReplicaCount:  1,
			Image:         appspec.ImageSpec{Repository: image, Tag: tag, PullPolicy: "IfNotPresent"},
			Service:       appspec.ServiceSpec{Port: port, TargetPort: port, Type: "ClusterIP"},
			ContainerPort: port,
			ServiceAccount: appspec.ServiceAccount{
				Name: "vso-" + namespace,
			},
			ImagePullSecret: registrySecret,
			Resources:       appspec.ResourceSpec{Profile: resourceProfile},
			Health:          appspec.HealthSpec{Path: "/healthz"},
			ImageAutomation: appspec.ImageAutomation{
				Enabled:                 boolPtr(withImageAutomation),
				TagPattern:              imagePolicyTagPattern,
				Order:                   imagePolicyOrder,
				GitSource:               fluxGitSource,
				GitNamespace:            fluxGitNamespace,
				ImageRepositoryInterval: imageRepositoryInterval,
				ImagePolicyInterval:     imagePolicyInterval,
				ImageAutomationInterval: imageAutomationInterval,
				HarborCASecret:          harborCASecret,
				RegistrySecret:          registrySecret,
			},
		},
	}
	issues := appspec.Validate(spec, appFile)
	if len(issues) > 0 {
		var parts []string
		for _, issue := range issues {
			parts = append(parts, issue.Error())
		}
		return fmt.Errorf("new service spec invalid: %s", strings.Join(parts, "; "))
	}

	if err := writeAppSpecFile(appFile, spec); err != nil {
		return err
	}
	if err := renderOne(root, appFile, spec); err != nil {
		return err
	}
	if err := ensureResourceInKustomization(layerKustomization, "apps/"+name); err != nil {
		return err
	}

	fmt.Printf("Created service app spec: %s\n", relPath(root, appFile))
	fmt.Printf("Updated layer kustomization: %s\n", relPath(root, layerKustomization))
	return nil
}

func runNewApp(args []string) error {
	fsNewApp := flag.NewFlagSet("new-app", flag.ContinueOnError)
	fsNewApp.SetOutput(os.Stderr)

	var (
		layer              string
		namespace          string
		app                string
		repoRoot           string
		fluxKSName         string
		dependsOnRaw       string
		registerKS         bool
		gatewayAccessLabel string
		vaultSecretPath    string
		withVaultScaffold  bool
		withVaultMR        bool
		vaultRepoRoot      string
		vaultRemote        string
		vaultBaseBranch    string
		vaultBranch        string
		vaultCommitMessage string
		vaultMRTitle       string
		vaultMRDescription string
		vaultProject       string
		vaultGitlabURL     string
		vaultGitlabToken   string
		auto               bool
		autoWaitCI         bool
		autoCITimeout      time.Duration
		autoCIPoll         time.Duration
		autoMerge          bool
		autoMergeRemoveSrc bool
		autoVerifyVault    bool
		autoVaultAddr      string
		autoVaultToken     string
		autoVaultTimeout   time.Duration
		autoVaultWait      time.Duration
		autoVaultPoll      time.Duration
		k8sRemote          string
		k8sBaseBranch      string
		k8sBranch          string
		k8sCommitMessage   string
		k8sMRTitle         string
		k8sMRDescription   string
		k8sProject         string
		k8sGitlabURL       string
		k8sGitlabToken     string
	)

	fsNewApp.StringVar(&layer, "layer", "", "new layer in format NN-name")
	fsNewApp.StringVar(&namespace, "namespace", "", "new namespace name")
	fsNewApp.StringVar(&app, "app", "", "application id (defaults to namespace)")
	fsNewApp.StringVar(&repoRoot, "repo-root", "", "override repository root")
	fsNewApp.StringVar(&fluxKSName, "flux-ks-name", "", "Flux Kustomization name (default homelab-<layer>)")
	fsNewApp.StringVar(&dependsOnRaw, "depends-on", "homelab-02-network", "comma-separated dependsOn names")
	fsNewApp.BoolVar(&registerKS, "register-ks", true, "register ks file in root flux kustomizations")
	fsNewApp.StringVar(&gatewayAccessLabel, "gateway-access-label", "allow", "namespace label gateway-access value")
	fsNewApp.StringVar(&vaultSecretPath, "vault-secret-path", "harbor/robots/dota2-assistant-runtime-pull", "Vault path for pull robot secret")
	fsNewApp.BoolVar(&withVaultScaffold, "with-vault-scaffold", false, "create roles.d/policies scaffold in vault-control-plane repository")
	fsNewApp.BoolVar(&withVaultMR, "with-vault-mr", false, "create/push branch and open merge request in vault-control-plane (requires --with-vault-scaffold)")
	fsNewApp.StringVar(&vaultRepoRoot, "vault-repo-root", "", "path to vault-control-plane repository (default ../vault-control-plane)")
	fsNewApp.StringVar(&vaultRemote, "vault-remote", "origin", "git remote name for vault-control-plane repository")
	fsNewApp.StringVar(&vaultBaseBranch, "vault-base-branch", "main", "target/base branch for vault-control-plane changes")
	fsNewApp.StringVar(&vaultBranch, "vault-branch", "", "source branch name for vault-control-plane changes (default auto-generated)")
	fsNewApp.StringVar(&vaultCommitMessage, "vault-commit-message", "", "commit message for vault-control-plane changes")
	fsNewApp.StringVar(&vaultMRTitle, "vault-mr-title", "", "merge request title for vault-control-plane changes")
	fsNewApp.StringVar(&vaultMRDescription, "vault-mr-description", "", "merge request description for vault-control-plane changes")
	fsNewApp.StringVar(&vaultProject, "vault-project", "cluster/vault-control-plane", "GitLab project id or path for vault-control-plane merge request API")
	fsNewApp.StringVar(&vaultGitlabURL, "vault-gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", "https://example.internal"), "GitLab base URL for merge request API")
	fsNewApp.StringVar(&vaultGitlabToken, "vault-gitlab-token", os.Getenv("PLATFORM_GITLAB_TOKEN"), "GitLab token for creating merge request (or set PLATFORM_GITLAB_TOKEN)")
	fsNewApp.BoolVar(&auto, "auto", false, "orchestrate k8s+vault merge requests and CI checks (safe mode, no auto-merge)")
	fsNewApp.BoolVar(&autoWaitCI, "auto-wait-ci", true, "wait for branch pipelines in vault-control-plane and k8s projects when --auto is enabled")
	fsNewApp.DurationVar(&autoCITimeout, "auto-ci-timeout", 45*time.Minute, "CI wait timeout for --auto")
	fsNewApp.DurationVar(&autoCIPoll, "auto-ci-poll-interval", 10*time.Second, "CI poll interval for --auto")
	fsNewApp.BoolVar(&autoMerge, "auto-merge", false, "when --auto is enabled, merge in order vault-control-plane -> verify Vault -> k8s")
	fsNewApp.BoolVar(&autoMergeRemoveSrc, "auto-merge-remove-source-branch", true, "remove source branch after successful auto merge")
	fsNewApp.BoolVar(&autoVerifyVault, "auto-verify-vault", true, "verify Vault role/policy after vault merge when --auto-merge is enabled")
	fsNewApp.StringVar(&autoVaultAddr, "vault-addr", firstNonEmptyEnv("VAULT_ADDR", "VAULT_ADDR_FOR_RUNNER"), "Vault address for --auto-verify-vault")
	fsNewApp.StringVar(&autoVaultToken, "vault-token", firstNonEmptyEnv("VAULT_TOKEN", "VAULT_TOKEN_FOR_RUNNER"), "Vault token for --auto-verify-vault")
	fsNewApp.DurationVar(&autoVaultTimeout, "vault-timeout", 15*time.Second, "Vault API request timeout for --auto-verify-vault")
	fsNewApp.DurationVar(&autoVaultWait, "auto-vault-verify-timeout", 10*time.Minute, "max wait for Vault role/policy visibility after vault merge")
	fsNewApp.DurationVar(&autoVaultPoll, "auto-vault-verify-poll-interval", 10*time.Second, "poll interval for Vault verification")
	fsNewApp.StringVar(&k8sRemote, "k8s-remote", "origin", "git remote for k8s merge request branch")
	fsNewApp.StringVar(&k8sBaseBranch, "k8s-base-branch", "master", "target/base branch for k8s merge request")
	fsNewApp.StringVar(&k8sBranch, "k8s-branch", "", "source branch for k8s merge request (default auto-generated)")
	fsNewApp.StringVar(&k8sCommitMessage, "k8s-commit-message", "", "commit message for k8s merge request")
	fsNewApp.StringVar(&k8sMRTitle, "k8s-mr-title", "", "merge request title for k8s repository")
	fsNewApp.StringVar(&k8sMRDescription, "k8s-mr-description", "", "merge request description for k8s repository")
	fsNewApp.StringVar(&k8sProject, "k8s-project", "cluster/k8s", "GitLab project id or path for k8s merge request API")
	fsNewApp.StringVar(&k8sGitlabURL, "k8s-gitlab-url", envOrDefault("PLATFORM_GITLAB_URL", "https://example.internal"), "GitLab base URL for k8s merge request API")
	fsNewApp.StringVar(&k8sGitlabToken, "k8s-gitlab-token", os.Getenv("PLATFORM_GITLAB_TOKEN"), "GitLab token for k8s merge request (or set PLATFORM_GITLAB_TOKEN)")

	if err := fsNewApp.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if strings.TrimSpace(layer) == "" || strings.TrimSpace(namespace) == "" {
		return errors.New("required flags: --layer --namespace")
	}
	if withVaultMR {
		withVaultScaffold = true
	}
	if auto {
		withVaultScaffold = true
		withVaultMR = true
	}
	if app == "" {
		app = namespace
	}
	if fluxKSName == "" {
		fluxKSName = "homelab-" + layer
	}
	if !layerNameRe.MatchString(layer) {
		return fmt.Errorf("invalid --layer: %s (expected NN-name)", layer)
	}
	if !dns1123NameRe.MatchString(namespace) {
		return fmt.Errorf("invalid --namespace: %s", namespace)
	}
	if !dns1123NameRe.MatchString(app) {
		return fmt.Errorf("invalid --app: %s", app)
	}
	if strings.TrimSpace(vaultBaseBranch) == "" {
		return errors.New("--vault-base-branch must not be empty")
	}
	if strings.TrimSpace(vaultProject) == "" {
		return errors.New("--vault-project must not be empty")
	}
	if strings.TrimSpace(k8sBaseBranch) == "" {
		return errors.New("--k8s-base-branch must not be empty")
	}
	if strings.TrimSpace(k8sProject) == "" {
		return errors.New("--k8s-project must not be empty")
	}
	if auto {
		if strings.TrimSpace(vaultGitlabToken) == "" {
			return errors.New("--auto requires --vault-gitlab-token or PLATFORM_GITLAB_TOKEN")
		}
		if strings.TrimSpace(k8sGitlabToken) == "" {
			return errors.New("--auto requires --k8s-gitlab-token or PLATFORM_GITLAB_TOKEN")
		}
		if autoCITimeout <= 0 {
			return errors.New("--auto-ci-timeout must be > 0")
		}
		if autoCIPoll <= 0 {
			return errors.New("--auto-ci-poll-interval must be > 0")
		}
		if autoMerge && !autoWaitCI {
			return errors.New("--auto-merge requires --auto-wait-ci=true")
		}
		if autoMerge && autoVerifyVault {
			if strings.TrimSpace(autoVaultAddr) == "" {
				return errors.New("--auto-verify-vault requires --vault-addr or VAULT_ADDR")
			}
			if strings.TrimSpace(autoVaultToken) == "" {
				return errors.New("--auto-verify-vault requires --vault-token or VAULT_TOKEN")
			}
			if autoVaultTimeout <= 0 {
				return errors.New("--vault-timeout must be > 0")
			}
			if autoVaultWait <= 0 {
				return errors.New("--auto-vault-verify-timeout must be > 0")
			}
			if autoVaultPoll <= 0 {
				return errors.New("--auto-vault-verify-poll-interval must be > 0")
			}
		}
	}
	if autoMerge && !auto {
		return errors.New("--auto-merge requires --auto")
	}

	root, err := resolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}

	layerDir := filepath.Join(root, "clusters", "homelab", layer)
	if _, err := os.Stat(layerDir); err == nil {
		return fmt.Errorf("layer already exists: %s", relPath(root, layerDir))
	}

	fluxRootKustomization := filepath.Join(root, "clusters", "homelab", "00-flux-ks", "kustomization.yaml")
	fluxSystemKustomization := filepath.Join(root, "clusters", "homelab", "flux-system", "kustomization.yaml")
	fluxKSFile := filepath.Join(root, "clusters", "homelab", "flux-system", "ks-"+layer+".yaml")

	for _, req := range []string{fluxRootKustomization, fluxSystemKustomization} {
		if _, err := os.Stat(req); err != nil {
			return fmt.Errorf("required file not found: %s", relPath(root, req))
		}
	}

	vsoServiceAccount := "vso-" + namespace
	vaultRole := vsoServiceAccount
	vaultAuthName := vsoServiceAccount

	if err := os.MkdirAll(filepath.Join(layerDir, "apps"), 0o755); err != nil {
		return fmt.Errorf("create layer directory: %w", err)
	}

	namespaceYAML := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
  labels:
    gateway-access: %q
    platform.adminwg.dad/managed-by: "platformctl"
    platform.adminwg.dad/app: %q
    platform.adminwg.dad/layer: %q
`, namespace, gatewayAccessLabel, app, layer)

	serviceAccountYAML := fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: %s
  namespace: %s
  annotations:
    registry.home.arpa/vault-role: %s
    registry.home.arpa/vault-secret-path: %s
imagePullSecrets:
  - name: harbor-proxy-robot
`, vsoServiceAccount, namespace, vaultRole, vaultSecretPath)

	vaultAuthYAML := fmt.Sprintf(`apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: %s
  namespace: %s
spec:
  method: kubernetes
  mount: kubernetes
  vaultConnectionRef: vault-secrets-operator/default
  kubernetes:
    role: %s
    serviceAccount: %s
    audiences:
      - https://kubernetes.default.svc
`, vaultAuthName, namespace, vaultRole, vsoServiceAccount)

	vaultStaticSecretYAML := fmt.Sprintf(`apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: harbor-proxy-robot
  namespace: %s
spec:
  type: kv-v2
  mount: kv
  path: %s
  refreshAfter: 1h
  hmacSecretData: true
  vaultAuthRef: %s
  destination:
    name: harbor-proxy-robot
    create: true
    overwrite: true
    type: kubernetes.io/dockerconfigjson
    labels:
      reconcile.fluxcd.io/watch: Enabled
    transformation:
      excludes:
        - ".*"
      templates:
        .dockerconfigjson:
          text: |
            {{- $u := get .Secrets "username" -}}
            {{- $p := get .Secrets "secret" -}}
            {{- dict "auths" (dict "harbor.home.arpa" (dict "username" $u "password" $p "auth" (printf "%%s:%%s" $u $p | b64enc))) | mustToJson -}}
`, namespace, vaultSecretPath, vaultAuthName)

	layerKustomizationYAML := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: %s
resources:
  - namespace.yaml
  - serviceaccount-vso.yaml
  - vaultauth-%s.yaml
  - vaultstaticsecret-harbor-pull.yaml
  - harbor-oci-ca.yaml
`, namespace, app)

	dependsOn := splitCSV(dependsOnRaw)
	fluxKSYAML := buildFluxKustomizationYAML(fluxKSName, layer, dependsOn)

	if err := os.WriteFile(filepath.Join(layerDir, "namespace.yaml"), []byte(namespaceYAML), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layerDir, "serviceaccount-vso.yaml"), []byte(serviceAccountYAML), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layerDir, "vaultauth-"+app+".yaml"), []byte(vaultAuthYAML), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layerDir, "vaultstaticsecret-harbor-pull.yaml"), []byte(vaultStaticSecretYAML), 0o644); err != nil {
		return err
	}
	harborCATemplate := filepath.Join(root, "clusters", "homelab", "09-dota2-assistant", "harbor-oci-ca.yaml")
	harborCAData, err := os.ReadFile(harborCATemplate)
	if err != nil {
		return fmt.Errorf("read harbor CA template %s: %w", relPath(root, harborCATemplate), err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "harbor-oci-ca.yaml"), harborCAData, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(layerDir, "kustomization.yaml"), []byte(layerKustomizationYAML), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(fluxKSFile, []byte(fluxKSYAML), 0o644); err != nil {
		return err
	}

	if registerKS {
		if err := ensureResourceInKustomization(fluxRootKustomization, "../flux-system/ks-"+layer+".yaml"); err != nil {
			return fmt.Errorf("register flux ks in 00-flux-ks: %w", err)
		}
		if err := ensureResourceInKustomization(fluxSystemKustomization, "ks-"+layer+".yaml"); err != nil {
			return fmt.Errorf("register flux ks in flux-system: %w", err)
		}
	}

	if withVaultScaffold {
		if vaultRepoRoot == "" {
			vaultRepoRoot = filepath.Join(filepath.Dir(root), "vault-control-plane")
		} else if !filepath.IsAbs(vaultRepoRoot) {
			vaultRepoRoot = filepath.Join(root, vaultRepoRoot)
		}
		if (withVaultMR || auto) && strings.TrimSpace(vaultBranch) == "" {
			vaultBranch = fmt.Sprintf("platformctl/new-app-%s-%s-vault", namespace, time.Now().UTC().Format("20060102150405"))
		}
		scaffoldCfg := vaultScaffoldConfig{
			VaultRepoRoot:   vaultRepoRoot,
			RoleName:        vaultRole,
			ServiceAccount:  vsoServiceAccount,
			Namespace:       namespace,
			SecretPath:      vaultSecretPath,
			EnableMR:        withVaultMR,
			Remote:          vaultRemote,
			BaseBranch:      vaultBaseBranch,
			Branch:          vaultBranch,
			CommitMessage:   vaultCommitMessage,
			MRTitle:         vaultMRTitle,
			MRDescription:   vaultMRDescription,
			GitLabProject:   vaultProject,
			GitLabURL:       vaultGitlabURL,
			GitLabToken:     vaultGitlabToken,
			SourceLayerName: layer,
		}
		scaffoldResult, err := scaffoldVaultControlPlane(scaffoldCfg)
		if err != nil {
			return err
		}
		for _, item := range scaffoldResult.Created {
			fmt.Printf("Created vault scaffold file: %s\n", item)
		}
		for _, item := range scaffoldResult.Updated {
			fmt.Printf("Updated vault scaffold file: %s\n", item)
		}
		if scaffoldResult.MRURL != "" {
			fmt.Printf("Created vault-control-plane merge request: %s\n", scaffoldResult.MRURL)
		}

		if auto {
			k8sPaths := []string{
				filepath.ToSlash(filepath.Join("clusters", "homelab", layer)),
				filepath.ToSlash(filepath.Join("clusters", "homelab", "flux-system", "ks-"+layer+".yaml")),
			}
			if registerKS {
				k8sPaths = append(k8sPaths,
					filepath.ToSlash(filepath.Join("clusters", "homelab", "00-flux-ks", "kustomization.yaml")),
					filepath.ToSlash(filepath.Join("clusters", "homelab", "flux-system", "kustomization.yaml")),
				)
			}
			autoCfg := newAppAutoConfig{
				RepoRoot:            root,
				Layer:               layer,
				Namespace:           namespace,
				App:                 app,
				K8sRemote:           k8sRemote,
				K8sBaseBranch:       k8sBaseBranch,
				K8sBranch:           k8sBranch,
				K8sCommitMessage:    k8sCommitMessage,
				K8sMRTitle:          k8sMRTitle,
				K8sMRDescription:    k8sMRDescription,
				K8sProject:          k8sProject,
				K8sGitLabURL:        k8sGitlabURL,
				K8sGitLabToken:      k8sGitlabToken,
				K8sPaths:            k8sPaths,
				VaultProject:        vaultProject,
				VaultBranch:         vaultBranch,
				VaultMRURL:          scaffoldResult.MRURL,
				VaultGitLabURL:      vaultGitlabURL,
				VaultGitLabToken:    vaultGitlabToken,
				WaitCI:              autoWaitCI,
				CITimeout:           autoCITimeout,
				CIPollInterval:      autoCIPoll,
				AutoMerge:           autoMerge,
				RemoveSourceBranch:  autoMergeRemoveSrc,
				VerifyVault:         autoMerge && autoVerifyVault,
				VaultAddr:           autoVaultAddr,
				VaultToken:          autoVaultToken,
				VaultRole:           vaultRole,
				VaultPolicy:         vaultRole,
				VaultRequestTimeout: autoVaultTimeout,
				VaultVerifyTimeout:  autoVaultWait,
				VaultVerifyPoll:     autoVaultPoll,
			}
			if err := runNewAppAuto(autoCfg); err != nil {
				return err
			}
		}
	}

	fmt.Printf("Created app layer: %s\n", relPath(root, layerDir))
	fmt.Printf("Created flux kustomization: %s\n", relPath(root, fluxKSFile))
	fmt.Println()
	if !withVaultScaffold {
		fmt.Printf("Mandatory TODO in vault-control-plane:\n")
		fmt.Printf("  1) Add policy: policies/%s.hcl\n", vaultRole)
		fmt.Printf("  2) Add role:\n")
		fmt.Printf("     role_name = %q\n", vaultRole)
		fmt.Printf("     bound_service_account_names = [%q]\n", vsoServiceAccount)
		fmt.Printf("     bound_service_account_namespaces = [%q]\n", namespace)
	}
	return nil
}

func writeAppSpecFile(path string, spec *appspec.ServiceApp) error {
	content, err := marshalYAMLWithIndent(spec, 2)
	if err != nil {
		return fmt.Errorf("marshal app spec: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write app spec: %w", err)
	}
	return nil
}

func ensureResourceInKustomization(path, resource string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse kustomization %s: %w", path, err)
	}

	var resources []string
	if raw, ok := doc["resources"]; ok {
		switch typed := raw.(type) {
		case []any:
			for _, item := range typed {
				s, ok := item.(string)
				if ok && strings.TrimSpace(s) != "" {
					resources = append(resources, s)
				}
			}
		case []string:
			resources = append(resources, typed...)
		}
	}

	for _, existing := range resources {
		if existing == resource {
			return nil
		}
	}

	resources = append(resources, resource)
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

func splitCSV(csv string) []string {
	parts := strings.Split(csv, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func buildFluxKustomizationYAML(name, layer string, dependsOn []string) string {
	var b strings.Builder
	b.WriteString("apiVersion: kustomize.toolkit.fluxcd.io/v1\n")
	b.WriteString("kind: Kustomization\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: ")
	b.WriteString(name)
	b.WriteString("\n")
	b.WriteString("  namespace: flux-system\n")
	b.WriteString("spec:\n")
	b.WriteString("  interval: 10m\n")
	b.WriteString("  retryInterval: 2m\n")
	b.WriteString("  timeout: 20m\n")
	b.WriteString("  path: ./clusters/homelab/")
	b.WriteString(layer)
	b.WriteString("\n")
	b.WriteString("  prune: true\n")
	b.WriteString("  sourceRef:\n")
	b.WriteString("    kind: GitRepository\n")
	b.WriteString("    name: homelab\n")
	if len(dependsOn) > 0 {
		b.WriteString("  dependsOn:\n")
		for _, dep := range dependsOn {
			b.WriteString("    - name: ")
			b.WriteString(dep)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func boolPtr(v bool) *bool {
	return &v
}
