package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureResourceInKustomization_AppendsOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kustomization.yaml")
	initial := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("write initial kustomization: %v", err)
	}

	if err := ensureResourceInKustomization(path, "apps/demo"); err != nil {
		t.Fatalf("append resource: %v", err)
	}
	if err := ensureResourceInKustomization(path, "apps/demo"); err != nil {
		t.Fatalf("append duplicate resource: %v", err)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(out)
	if strings.Count(text, "apps/demo") != 1 {
		t.Fatalf("expected single apps/demo entry, got:\n%s", text)
	}
}

func TestSplitCSV(t *testing.T) {
	values := splitCSV("a, b,, c ,")
	if len(values) != 3 {
		t.Fatalf("expected 3 items, got %d", len(values))
	}
	if values[0] != "a" || values[1] != "b" || values[2] != "c" {
		t.Fatalf("unexpected values: %#v", values)
	}
}
