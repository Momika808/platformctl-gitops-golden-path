package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldVaultControlPlane_CreatesRoleAndPolicy(t *testing.T) {
	vaultRepo := t.TempDir()
	if err := exec.Command("git", "init", "-q", vaultRepo).Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(vaultRepo, "main.tf"), []byte("terraform {}\n"), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(vaultRepo, "policies"), 0o755); err != nil {
		t.Fatalf("mkdir policies: %v", err)
	}

	cfg := vaultScaffoldConfig{
		VaultRepoRoot:  vaultRepo,
		RoleName:       "vso-demo",
		ServiceAccount: "vso-demo",
		Namespace:      "demo",
		SecretPath:     "harbor/robots/demo-pull",
	}

	result, err := scaffoldVaultControlPlane(cfg)
	if err != nil {
		t.Fatalf("scaffoldVaultControlPlane: %v", err)
	}
	if len(result.Created) != 2 {
		t.Fatalf("expected 2 created files, got %d", len(result.Created))
	}

	roleBody, err := os.ReadFile(filepath.Join(vaultRepo, "roles.d", "vso-demo.yaml"))
	if err != nil {
		t.Fatalf("read role file: %v", err)
	}
	if !strings.Contains(string(roleBody), "name: vso-demo") {
		t.Fatalf("role file missing expected name:\n%s", string(roleBody))
	}
	if !strings.Contains(string(roleBody), "policy_file: policies/vso-demo.hcl") {
		t.Fatalf("role file missing expected policy path:\n%s", string(roleBody))
	}

	policyBody, err := os.ReadFile(filepath.Join(vaultRepo, "policies", "vso-demo.hcl"))
	if err != nil {
		t.Fatalf("read policy file: %v", err)
	}
	if !strings.Contains(string(policyBody), `path "kv/data/harbor/robots/demo-pull"`) {
		t.Fatalf("policy file missing expected path:\n%s", string(policyBody))
	}
}
