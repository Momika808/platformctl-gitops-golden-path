package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSetAndRemoveResourceInKustomization(t *testing.T) {
	dir := t.TempDir()
	ks := filepath.Join(dir, "kustomization.yaml")
	initial := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - serviceaccount.yaml
`
	if err := os.WriteFile(ks, []byte(initial), 0o644); err != nil {
		t.Fatalf("write ks: %v", err)
	}

	if err := removeResourceInKustomization(ks, "serviceaccount.yaml"); err != nil {
		t.Fatalf("removeResourceInKustomization: %v", err)
	}
	data, err := os.ReadFile(ks)
	if err != nil {
		t.Fatalf("read ks after remove: %v", err)
	}
	if strings.Contains(string(data), "serviceaccount.yaml") {
		t.Fatalf("resource was not removed:\n%s", string(data))
	}

	if err := setKustomizationResources(ks, []string{}); err != nil {
		t.Fatalf("setKustomizationResources: %v", err)
	}
	data2, err := os.ReadFile(ks)
	if err != nil {
		t.Fatalf("read ks after set empty: %v", err)
	}
	if !strings.Contains(string(data2), "resources: []") {
		t.Fatalf("expected empty resources list, got:\n%s", string(data2))
	}
}

func TestBuildDeleteAppPlan(t *testing.T) {
	repo := t.TempDir()
	vaultRepo := t.TempDir()

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mustWrite(filepath.Join(repo, "clusters/homelab/15-demo/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/15-demo/namespace.yaml"), `apiVersion: v1
kind: Namespace
metadata:
  name: demo
  labels:
    platform.adminwg.dad/managed-by: platformctl
    platform.adminwg.dad/app: demo
    platform.adminwg.dad/layer: 15-demo
`)
	mustWrite(filepath.Join(repo, "clusters/homelab/flux-system/ks-15-demo.yaml"), "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/00-flux-ks/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/flux-system/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(vaultRepo, ".git/HEAD"), "ref: refs/heads/main\n")

	cfg := deleteAppConfig{
		Layer:         "15-demo",
		Namespace:     "demo",
		RepoRoot:      repo,
		VaultRepoRoot: vaultRepo,
	}
	plan, err := buildDeleteAppPlan(cfg)
	if err != nil {
		t.Fatalf("buildDeleteAppPlan: %v", err)
	}
	if !strings.HasSuffix(plan.VaultRoleFile, "roles.d/vso-demo.yaml") {
		t.Fatalf("unexpected role file: %s", plan.VaultRoleFile)
	}
	if !strings.HasSuffix(plan.K8sFluxLayerFile, "clusters/homelab/flux-system/ks-15-demo.yaml") {
		t.Fatalf("unexpected flux layer file: %s", plan.K8sFluxLayerFile)
	}
}

func TestVerifyOwnershipLabelsInLayer(t *testing.T) {
	repo := t.TempDir()
	vaultRepo := t.TempDir()

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mustWrite(filepath.Join(repo, "clusters/homelab/15-demo/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/15-demo/namespace.yaml"), `apiVersion: v1
kind: Namespace
metadata:
  name: demo
  labels:
    platform.adminwg.dad/managed-by: platformctl
    platform.adminwg.dad/app: demo
    platform.adminwg.dad/layer: 15-demo
`)
	mustWrite(filepath.Join(repo, "clusters/homelab/flux-system/ks-15-demo.yaml"), "apiVersion: kustomize.toolkit.fluxcd.io/v1\nkind: Kustomization\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/00-flux-ks/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(repo, "clusters/homelab/flux-system/kustomization.yaml"), "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n")
	mustWrite(filepath.Join(vaultRepo, ".git/HEAD"), "ref: refs/heads/main\n")

	cfg := deleteAppConfig{
		Layer:         "15-demo",
		Namespace:     "demo",
		App:           "demo",
		RepoRoot:      repo,
		VaultRepoRoot: vaultRepo,
	}
	plan, err := buildDeleteAppPlan(cfg)
	if err != nil {
		t.Fatalf("buildDeleteAppPlan: %v", err)
	}
	if err := verifyOwnershipLabelsInLayer(cfg, plan); err != nil {
		t.Fatalf("verifyOwnershipLabelsInLayer: %v", err)
	}
}

func TestDeleteStateSaveLoad(t *testing.T) {
	repo := t.TempDir()
	st := &deleteAppState{
		RequestID: "req-1",
		Status:    deleteStatusPlanned,
		CreatedAt: time.Now().UTC(),
		Layer:     "15-demo",
		Namespace: "demo",
		App:       "demo",
	}
	if err := saveDeleteAppState(repo, st); err != nil {
		t.Fatalf("saveDeleteAppState: %v", err)
	}
	loaded, err := loadDeleteAppState(repo, "req-1")
	if err != nil {
		t.Fatalf("loadDeleteAppState: %v", err)
	}
	if loaded.RequestID != "req-1" || loaded.Status != deleteStatusPlanned {
		t.Fatalf("unexpected loaded state: %+v", loaded)
	}

	raw, err := os.ReadFile(deleteAppStatePath(repo, "req-1"))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Fatalf("state file json invalid: %v", err)
	}
}
