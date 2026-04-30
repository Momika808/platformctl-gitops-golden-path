package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
	"gopkg.in/yaml.v3"
)

type doctorExternalConfig struct {
	VaultRepoRoot         string
	SkipVaultControlPlane bool
	SkipHarborImageCheck  bool
	SkipCapacityCheck     bool
	StrictCapacityCheck   bool
	HarborUsername        string
	HarborPassword        string
	HarborInsecure        bool
	HarborTimeout         time.Duration
}

type parsedImageRef struct {
	Registry string
	RepoPath string
	Ref      string
}

func (d *doctorChecker) checkVaultControlPlane(spec *appspec.ServiceApp, layerDir string) {
	if d.external.SkipVaultControlPlane {
		d.warn("Vault control-plane checks are disabled (--skip-vault-control-plane-check).")
		return
	}

	mainTF := filepath.Join(d.external.VaultRepoRoot, "main.tf")
	if _, err := os.Stat(mainTF); err != nil {
		d.fail(fmt.Sprintf("vault-control-plane main.tf not found: %s", d.external.VaultRepoRoot))
		return
	}

	roleName, err := readLayerVaultRole(layerDir)
	if err != nil {
		d.fail(fmt.Sprintf("cannot resolve Vault role from layer: %v", err))
		return
	}
	secretMount, secretPath, err := readLayerVaultSecretSource(layerDir)
	if err != nil {
		d.fail(fmt.Sprintf("cannot resolve Vault secret source from layer: %v", err))
		return
	}

	mainBody, err := os.ReadFile(mainTF)
	if err != nil {
		d.fail(fmt.Sprintf("cannot read vault-control-plane main.tf: %v", err))
		return
	}

	rolesDir := filepath.Join(d.external.VaultRepoRoot, "roles.d")
	if dirInfo, err := os.Stat(rolesDir); err == nil && dirInfo.IsDir() {
		d.checkVaultControlPlaneRolesDir(roleName, secretMount, secretPath, rolesDir)
		return
	}

	if !strings.Contains(string(mainBody), `role_name                        = "`+roleName+`"`) {
		d.fail(fmt.Sprintf("vault-control-plane missing kubernetes role_name=%s", roleName))
	} else {
		d.ok(fmt.Sprintf("vault-control-plane role exists in Terraform: %s", roleName))
	}
	if !strings.Contains(string(mainBody), `name   = "`+roleName+`"`) {
		d.fail(fmt.Sprintf("vault-control-plane missing policy resource name=%s", roleName))
	} else {
		d.ok(fmt.Sprintf("vault-control-plane policy resource exists: %s", roleName))
	}

	policyFile := filepath.Join(d.external.VaultRepoRoot, "policies", roleName+".hcl")
	if _, err := os.Stat(policyFile); err != nil {
		d.fail(fmt.Sprintf("vault policy file missing: %s", relPath(d.external.VaultRepoRoot, policyFile)))
		return
	}

	policyBody, err := os.ReadFile(policyFile)
	if err != nil {
		d.fail(fmt.Sprintf("cannot read vault policy file %s: %v", relPath(d.external.VaultRepoRoot, policyFile), err))
		return
	}

	expectedPath := fmt.Sprintf(`path "%s/data/%s"`, secretMount, secretPath)
	if !strings.Contains(string(policyBody), expectedPath) {
		d.fail(fmt.Sprintf("vault policy %s does not grant %s", roleName+".hcl", expectedPath))
		return
	}
	d.ok(fmt.Sprintf("vault policy grants required path: %s/data/%s", secretMount, secretPath))
}

func (d *doctorChecker) checkVaultControlPlaneRolesDir(roleName, secretMount, secretPath, rolesDir string) {
	roleFile := filepath.Join(rolesDir, roleName+".yaml")
	type roleDoc struct {
		Name                          string   `yaml:"name"`
		PolicyFile                    string   `yaml:"policy_file"`
		BoundServiceAccountNames      []string `yaml:"bound_service_account_names"`
		BoundServiceAccountNamespaces []string `yaml:"bound_service_account_namespaces"`
	}

	roleBody, err := os.ReadFile(roleFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			d.fail(fmt.Sprintf("vault-control-plane role file missing: %s", relPath(d.external.VaultRepoRoot, roleFile)))
			return
		}
		d.fail(fmt.Sprintf("cannot read role file %s: %v", relPath(d.external.VaultRepoRoot, roleFile), err))
		return
	}

	var doc roleDoc
	if err := yaml.Unmarshal(roleBody, &doc); err != nil {
		d.fail(fmt.Sprintf("cannot parse role file %s: %v", relPath(d.external.VaultRepoRoot, roleFile), err))
		return
	}
	if strings.TrimSpace(doc.Name) != roleName {
		d.fail(fmt.Sprintf("role file %s has mismatched name=%q expected=%q", relPath(d.external.VaultRepoRoot, roleFile), doc.Name, roleName))
		return
	}
	d.ok(fmt.Sprintf("vault-control-plane role exists in roles.d: %s", roleName))

	policyRel := strings.TrimSpace(doc.PolicyFile)
	if policyRel == "" {
		policyRel = filepath.ToSlash(filepath.Join("policies", roleName+".hcl"))
	}
	policyFile := filepath.Join(d.external.VaultRepoRoot, filepath.FromSlash(policyRel))
	if _, err := os.Stat(policyFile); err != nil {
		d.fail(fmt.Sprintf("vault policy file missing: %s", relPath(d.external.VaultRepoRoot, policyFile)))
		return
	}

	policyBody, err := os.ReadFile(policyFile)
	if err != nil {
		d.fail(fmt.Sprintf("cannot read vault policy file %s: %v", relPath(d.external.VaultRepoRoot, policyFile), err))
		return
	}
	expectedPath := fmt.Sprintf(`path "%s/data/%s"`, secretMount, secretPath)
	if !strings.Contains(string(policyBody), expectedPath) {
		d.fail(fmt.Sprintf("vault policy %s does not grant %s", relPath(d.external.VaultRepoRoot, policyFile), expectedPath))
		return
	}
	d.ok(fmt.Sprintf("vault policy grants required path: %s/data/%s", secretMount, secretPath))
}

func (d *doctorChecker) checkHarborImage(spec *appspec.ServiceApp) {
	if d.external.SkipHarborImageCheck {
		d.warn("Harbor image checks are disabled (--skip-harbor-image-check).")
		return
	}

	ref, err := parseImageRef(spec.Spec.Image)
	if err != nil {
		d.fail(fmt.Sprintf("invalid image reference for Harbor check: %v", err))
		return
	}
	if ref.Registry == "" || ref.RepoPath == "" || ref.Ref == "" {
		d.fail("image reference is incomplete for Harbor check")
		return
	}
	if d.external.HarborUsername == "" || d.external.HarborPassword == "" {
		d.fail("Harbor credentials are required for image checks (set --harbor-username/--harbor-password or PLATFORM_HARBOR_USERNAME/PLATFORM_HARBOR_PASSWORD)")
		return
	}
	if err := checkRegistryManifestExists(ref, d.external); err != nil {
		d.fail(err.Error())
		return
	}
	d.ok(fmt.Sprintf("image exists in registry: %s/%s@%s", ref.Registry, ref.RepoPath, ref.Ref))
}

func parseImageRef(image appspec.ImageSpec) (parsedImageRef, error) {
	repo := strings.TrimSpace(image.Repository)
	if repo == "" {
		return parsedImageRef{}, fmt.Errorf("image.repository is empty")
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return parsedImageRef{}, fmt.Errorf("image.repository must include registry host (got %q)", repo)
	}
	registry := strings.TrimSpace(parts[0])
	repoPath := strings.TrimSpace(parts[1])
	if registry == "" || repoPath == "" {
		return parsedImageRef{}, fmt.Errorf("invalid image.repository %q", repo)
	}

	ref := strings.TrimSpace(image.Digest)
	if ref == "" {
		ref = strings.TrimSpace(image.Tag)
	}
	if ref == "" {
		return parsedImageRef{}, fmt.Errorf("image.tag or image.digest is required")
	}

	return parsedImageRef{
		Registry: registry,
		RepoPath: repoPath,
		Ref:      ref,
	}, nil
}

func checkRegistryManifestExists(ref parsedImageRef, cfg doctorExternalConfig) error {
	timeout := cfg.HarborTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	scheme := "https"
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", scheme, ref.Registry, ref.RepoPath, url.PathEscape(ref.Ref))

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.HarborInsecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	req, err := http.NewRequest(http.MethodHead, manifestURL, nil)
	if err != nil {
		return fmt.Errorf("build manifest request: %w", err)
	}
	req.SetBasicAuth(cfg.HarborUsername, cfg.HarborPassword)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
	}, ", "))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Harbor manifest check failed for %s/%s:%s: %w", ref.Registry, ref.RepoPath, ref.Ref, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("Harbor manifest check unauthorized for %s/%s:%s (HTTP %d)", ref.Registry, ref.RepoPath, ref.Ref, resp.StatusCode)
	case http.StatusNotFound:
		return fmt.Errorf("image manifest not found in registry: %s/%s:%s", ref.Registry, ref.RepoPath, ref.Ref)
	default:
		return fmt.Errorf("unexpected Harbor response for %s/%s:%s: HTTP %d", ref.Registry, ref.RepoPath, ref.Ref, resp.StatusCode)
	}
}

func readLayerVaultRole(layerDir string) (string, error) {
	roleRegex := regexp.MustCompile(`(?m)^\s*role:\s*([^\s#]+)\s*$`)
	matches, err := filepath.Glob(filepath.Join(layerDir, "vaultauth-*.yaml"))
	if err != nil {
		return "", err
	}
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		found := roleRegex.FindStringSubmatch(string(body))
		if len(found) > 1 {
			return strings.TrimSpace(found[1]), nil
		}
	}
	return "", fmt.Errorf("no role found in vaultauth-*.yaml")
}

func readLayerVaultSecretSource(layerDir string) (mount, path string, err error) {
	matches, err := filepath.Glob(filepath.Join(layerDir, "vaultstaticsecret-*.yaml"))
	if err != nil {
		return "", "", err
	}

	type vaultStaticSecret struct {
		Kind string `yaml:"kind"`
		Spec struct {
			Mount string `yaml:"mount"`
			Path  string `yaml:"path"`
		} `yaml:"spec"`
	}

	for _, file := range matches {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", err
		}
		var doc vaultStaticSecret
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return "", "", fmt.Errorf("parse %s: %w", relPath(layerDir, file), err)
		}
		if doc.Kind != "VaultStaticSecret" {
			continue
		}
		if strings.TrimSpace(doc.Spec.Mount) == "" || strings.TrimSpace(doc.Spec.Path) == "" {
			continue
		}
		return strings.TrimSpace(doc.Spec.Mount), strings.TrimSpace(doc.Spec.Path), nil
	}
	return "", "", fmt.Errorf("no VaultStaticSecret with mount/path found in layer")
}
