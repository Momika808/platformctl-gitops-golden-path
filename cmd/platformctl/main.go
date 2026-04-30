package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errors.New("command is required")
	}

	switch args[0] {
	case "validate":
		return runValidate(args[1:])
	case "render":
		return runRender(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	case "infra":
		return runInfra(args[1:])
	case "new-app":
		return runNewApp(args[1:])
	case "new-service":
		return runNewService(args[1:])
	case "delete-app":
		return runDeleteApp(args[1:])
	case "export-public":
		return runExportPublic(args[1:])
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func runValidate(args []string) error {
	fsValidate := flag.NewFlagSet("validate", flag.ContinueOnError)
	fsValidate.SetOutput(os.Stderr)

	var (
		all      bool
		appFile  string
		repoRoot string
	)

	fsValidate.BoolVar(&all, "all", false, "validate all app specs")
	fsValidate.StringVar(&appFile, "app", "", "validate single app spec file")
	fsValidate.StringVar(&repoRoot, "repo-root", "", "override repository root")

	if err := fsValidate.Parse(args); err != nil {
		return err
	}

	if (all && appFile != "") || (!all && appFile == "") {
		return errors.New("use exactly one mode: --all or --app <path>")
	}

	root, err := resolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}

	var files []string
	if all {
		files, err = findAllAppSpecs(root)
		if err != nil {
			return err
		}
	} else {
		file := appFile
		if !filepath.IsAbs(file) {
			file = filepath.Join(root, file)
		}
		files = []string{file}
	}

	if len(files) == 0 {
		fmt.Println("No ServiceApp files found (clusters/homelab/*/apps/*/app.yaml).")
		return nil
	}

	fmt.Printf("Validating %d ServiceApp file(s)...\n", len(files))

	for _, file := range files {
		spec, err := appspec.Load(file)
		if err != nil {
			return fmt.Errorf("%s: %w", relPath(root, file), err)
		}

		issues := appspec.Validate(spec, file)
		if len(issues) > 0 {
			var parts []string
			for _, issue := range issues {
				parts = append(parts, issue.Error())
			}
			return fmt.Errorf("%s: %s", relPath(root, file), strings.Join(parts, "; "))
		}

		fmt.Printf("  OK  %s\n", relPath(root, file))
	}

	fmt.Println("ServiceApp schema validation passed.")
	return nil
}

func resolveRepoRoot(flagValue string) (string, error) {
	if flagValue != "" {
		abs, err := filepath.Abs(flagValue)
		if err != nil {
			return "", fmt.Errorf("resolve repo root: %w", err)
		}
		return abs, nil
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	cur := wd
	for {
		gitDir := filepath.Join(cur, ".git")
		info, err := os.Stat(gitDir)
		if err == nil && info.IsDir() {
			return cur, nil
		}
		if !errors.Is(err, os.ErrNotExist) && err != nil {
			return "", err
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	return "", errors.New("repository root not found (cannot locate .git)")
}

func findAllAppSpecs(repoRoot string) ([]string, error) {
	base := filepath.Join(repoRoot, "clusters", "homelab")
	if _, err := os.Stat(base); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) != "app.yaml" {
			return nil
		}
		normalized := filepath.ToSlash(path)
		if strings.Contains(normalized, "/apps/") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func findLayerAppSpecs(repoRoot, layer string) ([]string, error) {
	layerDir := filepath.Join(repoRoot, "clusters", "homelab", layer, "apps")
	if _, err := os.Stat(layerDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	err := filepath.WalkDir(layerDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "app.yaml" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, "platformctl - GitOps helper CLI")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  platformctl validate --all [--repo-root <path>]")
	fmt.Fprintln(out, "  platformctl validate --app <path> [--repo-root <path>]")
	fmt.Fprintln(out, "  platformctl render --all [--repo-root <path>] [--skip-validate]")
	fmt.Fprintln(out, "  platformctl render --app <path> [--repo-root <path>] [--skip-validate]")
	fmt.Fprintln(out, "  platformctl doctor --all [--repo-root <path>]")
	fmt.Fprintln(out, "  platformctl doctor --app <path> [--repo-root <path>]")
	fmt.Fprintln(out, "  platformctl doctor --layer <layer> [--repo-root <path>]")
	fmt.Fprintln(out, "  platformctl infra kubelet-provider run [flags]")
	fmt.Fprintln(out, "  platformctl infra kubelet-provider status [flags]")
	fmt.Fprintln(out, "  platformctl infra kubelet-provider logs [flags]")
	fmt.Fprintln(out, "  platformctl new-app --layer <NN-name> --namespace <ns> [flags]")
	fmt.Fprintln(out, "  platformctl new-service --layer <NN-name> --namespace <ns> --name <svc> --image <repo> --tag <tag> --port <port> [flags]")
	fmt.Fprintln(out, "  platformctl delete-app --layer <NN-name> --namespace <ns> [flags]")
	fmt.Fprintln(out, "  platformctl export-public --out <dir> [--module github.com/<org>/platformctl]")
}
