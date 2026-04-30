package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const internalModulePath = "github.com/Momika808/platformctl-gitops-golden-path"

func runExportPublic(args []string) error {
	fsExport := flag.NewFlagSet("export-public", flag.ContinueOnError)
	fsExport.SetOutput(os.Stderr)

	var (
		outDir   string
		module   string
		repoRoot string
		force    bool
	)

	fsExport.StringVar(&outDir, "out", "", "destination directory for sanitized public repo")
	fsExport.StringVar(&module, "module", "github.com/example/platformctl", "public Go module path")
	fsExport.StringVar(&repoRoot, "repo-root", "", "override repository root")
	fsExport.BoolVar(&force, "force", false, "allow non-empty destination directory")

	if err := fsExport.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(outDir) == "" {
		return errors.New("--out is required")
	}
	if strings.TrimSpace(module) == "" {
		return errors.New("--module is required")
	}

	root, err := resolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}

	srcRoot := filepath.Join(root, "tools", "platformctl")
	if _, err := os.Stat(srcRoot); err != nil {
		return fmt.Errorf("platformctl source not found: %s", srcRoot)
	}

	dest := outDir
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(root, dest)
	}

	if err := ensureDestination(dest, force); err != nil {
		return err
	}

	allowedRoots := map[string]bool{
		"cmd":      true,
		"internal": true,
	}
	allowedFiles := map[string]bool{
		"go.mod":    true,
		"go.sum":    true,
		"README.md": true,
	}

	err = filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		parts := strings.Split(rel, "/")
		top := parts[0]
		if d.IsDir() {
			if rel == "bin" {
				return filepath.SkipDir
			}
			if !allowedRoots[top] {
				return filepath.SkipDir
			}
			return nil
		}
		if !allowedRoots[top] && !allowedFiles[rel] {
			return nil
		}

		dstPath := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rewritten := rewriteForPublic(string(content), module)
		if err := os.WriteFile(dstPath, []byte(rewritten), 0o644); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	fmt.Printf("Public export created: %s\n", dest)
	fmt.Println("Next:")
	fmt.Println("  cd", dest)
	fmt.Println("  git init && git add . && git commit -m \"init: public platformctl\"")
	return nil
}

func ensureDestination(dest string, force bool) error {
	info, err := os.Stat(dest)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(dest, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("destination is not directory: %s", dest)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("destination is not empty: %s (use --force)", dest)
	}
	return nil
}

func rewriteForPublic(content, module string) string {
	out := content
	out = strings.ReplaceAll(out, internalModulePath, module)
	// Keep backward-compatible rewrites so repeated exports remain safe
	// even when source text still contains legacy internal references.
	out = strings.ReplaceAll(out, "gitlab.adminwg.dad/cluster/k8s/tools/platformctl", module)
	out = strings.ReplaceAll(out, "/Users/robot/Documents/Claster/k8s", "<repo-root>")
	out = strings.ReplaceAll(out, "gitlab.adminwg.dad", "example.internal")
	return out
}
