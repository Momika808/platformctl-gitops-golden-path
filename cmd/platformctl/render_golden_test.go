package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
)

func TestRenderGoldenBasic(t *testing.T) {
	fixtureRoot := filepath.Join("..", "..", "testdata", "render", "basic")
	inputRepo := filepath.Join(fixtureRoot, "repo")
	expectedDir := filepath.Join(fixtureRoot, "expected")

	tempRepo := filepath.Join(t.TempDir(), "repo")
	if err := copyDir(inputRepo, tempRepo); err != nil {
		t.Fatalf("copy fixture repo: %v", err)
	}

	appFile := filepath.Join(tempRepo, "clusters", "homelab", "11-rag", "apps", "demo", "app.yaml")
	spec, err := appspec.Load(appFile)
	if err != nil {
		t.Fatalf("load app spec: %v", err)
	}

	if err := renderOne(tempRepo, appFile, spec); err != nil {
		t.Fatalf("render: %v", err)
	}

	actualDir := filepath.Join(tempRepo, "clusters", "homelab", "11-rag", "apps", "demo")
	assertGoldenEqual(t, filepath.Join(expectedDir, "app-kustomization.yaml"), filepath.Join(actualDir, "kustomization.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "values.yaml"), filepath.Join(actualDir, "generated", "values.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "helmrelease.yaml"), filepath.Join(actualDir, "generated", "helmrelease.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "kustomization.yaml"), filepath.Join(actualDir, "generated", "kustomization.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "imagerepository.yaml"), filepath.Join(actualDir, "generated", "imagerepository.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "imagepolicy.yaml"), filepath.Join(actualDir, "generated", "imagepolicy.yaml"))
	assertGoldenEqual(t, filepath.Join(expectedDir, "imageupdateautomation.yaml"), filepath.Join(actualDir, "generated", "imageupdateautomation.yaml"))
}

func assertGoldenEqual(t *testing.T, expectedPath, actualPath string) {
	t.Helper()
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read expected %s: %v", expectedPath, err)
	}
	actual, err := os.ReadFile(actualPath)
	if err != nil {
		t.Fatalf("read actual %s: %v", actualPath, err)
	}
	if string(expected) != string(actual) {
		t.Fatalf("golden mismatch for %s\n--- expected ---\n%s\n--- actual ---\n%s", actualPath, string(expected), string(actual))
	}
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
