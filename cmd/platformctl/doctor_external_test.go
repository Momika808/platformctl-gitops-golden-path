package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
)

func TestParseImageRef(t *testing.T) {
	spec := appspec.ImageSpec{
		Repository: "harbor.home.arpa/org/app",
		Tag:        "main",
	}
	ref, err := parseImageRef(spec)
	if err != nil {
		t.Fatalf("parse image ref: %v", err)
	}
	if ref.Registry != "harbor.home.arpa" || ref.RepoPath != "org/app" || ref.Ref != "main" {
		t.Fatalf("unexpected image ref: %#v", ref)
	}

	spec = appspec.ImageSpec{
		Repository: "harbor.home.arpa/org/app",
		Digest:     "sha256:abc",
	}
	ref, err = parseImageRef(spec)
	if err != nil {
		t.Fatalf("parse image ref by digest: %v", err)
	}
	if ref.Ref != "sha256:abc" {
		t.Fatalf("digest ref expected, got %s", ref.Ref)
	}
}

func TestReadLayerVaultRoleAndSecretSource(t *testing.T) {
	layerDir := t.TempDir()
	vaultAuth := `apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: vso-demo
spec:
  kubernetes:
    role: vso-demo
`
	vaultStatic := `apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: harbor-proxy-robot
spec:
  mount: kv
  path: harbor/robots/demo
`
	if err := os.WriteFile(filepath.Join(layerDir, "vaultauth-demo.yaml"), []byte(vaultAuth), 0o644); err != nil {
		t.Fatalf("write vaultauth fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "vaultstaticsecret-harbor-pull.yaml"), []byte(vaultStatic), 0o644); err != nil {
		t.Fatalf("write vaultstaticsecret fixture: %v", err)
	}

	role, err := readLayerVaultRole(layerDir)
	if err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != "vso-demo" {
		t.Fatalf("unexpected role: %s", role)
	}

	mount, path, err := readLayerVaultSecretSource(layerDir)
	if err != nil {
		t.Fatalf("read secret source: %v", err)
	}
	if mount != "kv" || path != "harbor/robots/demo" {
		t.Fatalf("unexpected secret source: mount=%s path=%s", mount, path)
	}
}

func TestCheckRegistryManifestExists(t *testing.T) {
	user := "robot"
	pass := "secret"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v2/org/app/manifests/main" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	cfg := doctorExternalConfig{
		HarborUsername: user,
		HarborPassword: pass,
		HarborInsecure: true,
		HarborTimeout:  3 * time.Second,
	}
	ref := parsedImageRef{
		Registry: parsedURL.Host,
		RepoPath: "org/app",
		Ref:      "main",
	}
	if err := checkRegistryManifestExists(ref, cfg); err != nil {
		t.Fatalf("check manifest exists: %v", err)
	}
}

func TestCheckRegistryManifestExistsNotFound(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	cfg := doctorExternalConfig{
		HarborUsername: "u",
		HarborPassword: "p",
		HarborInsecure: true,
		HarborTimeout:  3 * time.Second,
	}
	ref := parsedImageRef{
		Registry: parsedURL.Host,
		RepoPath: "org/app",
		Ref:      "main",
	}
	err = checkRegistryManifestExists(ref, cfg)
	if err == nil {
		t.Fatalf("expected not found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckVaultControlPlaneRolesDir(t *testing.T) {
	vaultRepo := t.TempDir()
	rolesDir := filepath.Join(vaultRepo, "roles.d")
	policiesDir := filepath.Join(vaultRepo, "policies")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatalf("mkdir roles.d: %v", err)
	}
	if err := os.MkdirAll(policiesDir, 0o755); err != nil {
		t.Fatalf("mkdir policies: %v", err)
	}

	roleFile := `name: vso-demo
policy_file: policies/vso-demo.hcl
bound_service_account_names:
  - vso-demo
bound_service_account_namespaces:
  - demo
`
	if err := os.WriteFile(filepath.Join(rolesDir, "vso-demo.yaml"), []byte(roleFile), 0o644); err != nil {
		t.Fatalf("write role file: %v", err)
	}
	policyFile := `path "kv/data/harbor/robots/demo-pull" {
  capabilities = ["read"]
}
`
	if err := os.WriteFile(filepath.Join(policiesDir, "vso-demo.hcl"), []byte(policyFile), 0o644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	checker := &doctorChecker{
		external: doctorExternalConfig{VaultRepoRoot: vaultRepo},
	}
	checker.checkVaultControlPlaneRolesDir("vso-demo", "kv", "harbor/robots/demo-pull", rolesDir)
	if checker.failures != 0 {
		t.Fatalf("expected no failures, got %d", checker.failures)
	}
}
