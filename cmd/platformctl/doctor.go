package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Momika808/platformctl-gitops-golden-path/internal/appspec"
	"gopkg.in/yaml.v3"
)

func runDoctor(args []string) error {
	fsDoctor := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fsDoctor.SetOutput(os.Stderr)

	var (
		all                  bool
		appFile              string
		layer                string
		repoRoot             string
		skipVaultControlPlan bool
		skipHarborImageCheck bool
		skipCapacityCheck    bool
		strictCapacityCheck  bool
		vaultRepoRoot        string
		harborUsername       string
		harborPassword       string
		harborInsecure       bool
		harborTimeoutRaw     string
	)

	fsDoctor.BoolVar(&all, "all", false, "check all app specs")
	fsDoctor.StringVar(&appFile, "app", "", "check single app spec")
	fsDoctor.StringVar(&layer, "layer", "", "check app specs in layer")
	fsDoctor.StringVar(&repoRoot, "repo-root", "", "override repository root")
	fsDoctor.StringVar(&vaultRepoRoot, "vault-repo-root", envOrDefault("PLATFORM_VAULT_CONTROL_PLANE_ROOT", ""), "path to vault-control-plane repository")
	fsDoctor.BoolVar(&skipVaultControlPlan, "skip-vault-control-plane-check", false, "skip vault-control-plane role/policy checks")
	fsDoctor.BoolVar(&skipHarborImageCheck, "skip-harbor-image-check", false, "skip Harbor image existence checks")
	fsDoctor.BoolVar(&skipCapacityCheck, "skip-capacity-check", false, "skip cluster capacity preflight check")
	fsDoctor.BoolVar(&strictCapacityCheck, "strict-capacity-check", false, "fail if capacity check cannot be executed")
	fsDoctor.StringVar(&harborUsername, "harbor-username", envOrDefault("PLATFORM_HARBOR_USERNAME", ""), "Harbor API username (or env PLATFORM_HARBOR_USERNAME)")
	fsDoctor.StringVar(&harborPassword, "harbor-password", envOrDefault("PLATFORM_HARBOR_PASSWORD", ""), "Harbor API password/token (or env PLATFORM_HARBOR_PASSWORD)")
	fsDoctor.BoolVar(&harborInsecure, "harbor-insecure", envBool("PLATFORM_HARBOR_INSECURE"), "skip TLS verification for Harbor API")
	fsDoctor.StringVar(&harborTimeoutRaw, "harbor-timeout", envOrDefault("PLATFORM_HARBOR_TIMEOUT", "10s"), "HTTP timeout for Harbor API checks")

	if err := fsDoctor.Parse(args); err != nil {
		return err
	}

	modeCount := 0
	if all {
		modeCount++
	}
	if appFile != "" {
		modeCount++
	}
	if layer != "" {
		modeCount++
	}
	if modeCount != 1 {
		return errors.New("use exactly one mode: --all or --app <path> or --layer <name>")
	}

	if _, err := exec.LookPath("kubectl"); err != nil {
		return errors.New("kubectl is required for doctor checks")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is required for doctor checks")
	}

	root, err := resolveRepoRoot(repoRoot)
	if err != nil {
		return err
	}
	if vaultRepoRoot == "" {
		vaultRepoRoot = filepath.Join(filepath.Dir(root), "vault-control-plane")
	}
	if !filepath.IsAbs(vaultRepoRoot) {
		vaultRepoRoot = filepath.Join(root, vaultRepoRoot)
	}
	harborTimeout, err := time.ParseDuration(harborTimeoutRaw)
	if err != nil {
		return fmt.Errorf("invalid --harbor-timeout: %w", err)
	}

	var apps []string
	switch {
	case all:
		apps, err = findAllAppSpecs(root)
		if err != nil {
			return err
		}
	case appFile != "":
		path := appFile
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("app spec not found: %s", path)
		}
		apps = []string{path}
	case layer != "":
		apps, err = findLayerAppSpecs(root, layer)
		if err != nil {
			return err
		}
	}

	checker := &doctorChecker{
		repoRoot: root,
		external: doctorExternalConfig{
			VaultRepoRoot:         vaultRepoRoot,
			SkipVaultControlPlane: skipVaultControlPlan,
			SkipHarborImageCheck:  skipHarborImageCheck,
			SkipCapacityCheck:     skipCapacityCheck,
			StrictCapacityCheck:   strictCapacityCheck,
			HarborUsername:        harborUsername,
			HarborPassword:        harborPassword,
			HarborInsecure:        harborInsecure,
			HarborTimeout:         harborTimeout,
		},
	}
	if len(apps) == 0 {
		checker.warn("No app.yaml files found for selected scope.")
		if layer != "" {
			checker.checkLayerKustomizeBuild(layer)
		}
		return checker.result()
	}

	for _, app := range apps {
		if err := checker.checkServiceApp(app); err != nil {
			return err
		}
	}

	return checker.result()
}

type doctorChecker struct {
	repoRoot string
	failures int
	external doctorExternalConfig
}

func (d *doctorChecker) ok(msg string) {
	fmt.Printf("[OK] %s\n", msg)
}

func (d *doctorChecker) warn(msg string) {
	fmt.Fprintf(os.Stderr, "[WARN] %s\n", msg)
}

func (d *doctorChecker) fail(msg string) {
	fmt.Fprintf(os.Stderr, "[FAIL] %s\n", msg)
	d.failures++
}

func (d *doctorChecker) result() error {
	if d.failures > 0 {
		return fmt.Errorf("doctor finished with %d failure(s)", d.failures)
	}
	fmt.Println()
	fmt.Println("Doctor finished successfully.")
	return nil
}

func (d *doctorChecker) checkServiceApp(appFile string) error {
	relApp := relPath(d.repoRoot, appFile)
	fmt.Printf("== Checking %s\n", relApp)

	spec, err := appspec.Load(appFile)
	if err != nil {
		d.fail(fmt.Sprintf("cannot read app spec: %v", err))
		return nil
	}
	issues := appspec.Validate(spec, appFile)
	if len(issues) > 0 {
		var parts []string
		for _, issue := range issues {
			parts = append(parts, issue.Error())
		}
		d.fail(fmt.Sprintf("schema invalid: %s", strings.Join(parts, "; ")))
		return nil
	}
	d.ok(fmt.Sprintf("schema valid: %s", relApp))

	if err := renderOne(d.repoRoot, appFile, spec); err != nil {
		d.fail(fmt.Sprintf("render failed: %v", err))
		return nil
	}

	appDir := filepath.Dir(appFile)
	generatedDir := filepath.Join(appDir, "generated")

	drift, err := gitStatusPorcelain(generatedDir)
	if err != nil {
		d.fail(fmt.Sprintf("git status failed: %v", err))
	} else if strings.TrimSpace(drift) != "" {
		d.fail(fmt.Sprintf("generated drift detected for %s; run platformctl render and commit generated/*", relApp))
	} else {
		d.ok(fmt.Sprintf("generated is in sync: %s", relApp))
	}

	requiredGenerated := []string{"kustomization.yaml", "helmrelease.yaml", "values.yaml"}
	allPresent := true
	for _, file := range requiredGenerated {
		if _, err := os.Stat(filepath.Join(generatedDir, file)); err != nil {
			allPresent = false
			break
		}
	}
	if allPresent {
		d.ok(fmt.Sprintf("generated manifests present: %s", relPath(d.repoRoot, generatedDir)))
	} else {
		d.fail(fmt.Sprintf("generated manifests missing for %s", relApp))
	}

	layer := spec.Spec.Layer
	layerDir := filepath.Join(d.repoRoot, "clusters", "homelab", layer)
	layerKustomization := filepath.Join(layerDir, "kustomization.yaml")
	if _, err := os.Stat(layerKustomization); err != nil {
		d.fail(fmt.Sprintf("layer kustomization not found: %s", relPath(d.repoRoot, layerKustomization)))
	} else {
		resources, err := readKustomizationResources(layerKustomization)
		if err != nil {
			d.fail(fmt.Sprintf("cannot parse layer kustomization: %v", err))
		} else if contains(resources, "apps/"+spec.Metadata.Name) {
			d.ok(fmt.Sprintf("layer references app path: apps/%s", spec.Metadata.Name))
		} else {
			d.fail(fmt.Sprintf("layer does not include apps/%s: %s", spec.Metadata.Name, relPath(d.repoRoot, layerKustomization)))
		}
	}

	nsFile := filepath.Join(layerDir, "namespace.yaml")
	if _, err := os.Stat(nsFile); err != nil {
		d.fail(fmt.Sprintf("namespace file missing: %s", relPath(d.repoRoot, nsFile)))
	} else {
		nsName, gatewayAccess, err := readNamespaceInfo(nsFile)
		if err != nil {
			d.fail(fmt.Sprintf("cannot parse namespace.yaml: %v", err))
		} else {
			if nsName == spec.Spec.Namespace {
				d.ok(fmt.Sprintf("namespace match in layer: %s", spec.Spec.Namespace))
			} else {
				d.fail(fmt.Sprintf("namespace mismatch: app=%s, layer namespace.yaml=%s", spec.Spec.Namespace, nsName))
			}

			if isEnabled(spec.Spec.Route.Enabled) {
				if gatewayAccess == "allow" {
					d.ok("namespace gateway-access label is allow")
				} else {
					d.fail("route enabled but namespace gateway-access label is not allow")
				}
			}
		}
	}

	if isEnabled(spec.Spec.Route.Enabled) {
		certFile := filepath.Join(d.repoRoot, "clusters", "homelab", "ingress", "certificates", "gateway-internal-tls.yaml")
		hosts, err := readCertificateSANs(certFile)
		if err != nil {
			d.fail(fmt.Sprintf("cannot parse gateway certificate: %v", err))
		} else if contains(hosts, spec.Spec.Route.Host) {
			d.ok(fmt.Sprintf("route host present in gateway cert SAN: %s", spec.Spec.Route.Host))
		} else {
			d.fail(fmt.Sprintf("route host missing in gateway cert SAN: %s", spec.Spec.Route.Host))
		}
	}

	objects, err := scanKubeObjects(layerDir)
	if err != nil {
		d.fail(fmt.Sprintf("cannot scan layer objects: %v", err))
	} else {
		ns := spec.Spec.Namespace
		if hasKindName(objects, "VaultAuth", "vso-"+ns) {
			d.ok(fmt.Sprintf("VaultAuth exists for namespace: vso-%s", ns))
		} else {
			d.fail(fmt.Sprintf("VaultAuth missing for namespace %s (expected name vso-%s)", ns, ns))
		}

		if hasKindName(objects, "VaultStaticSecret", "harbor-proxy-robot") {
			d.ok("VaultStaticSecret harbor-proxy-robot exists")
		} else {
			d.fail(fmt.Sprintf("VaultStaticSecret harbor-proxy-robot missing for layer %s", layer))
		}

		serviceAccount := defaultString(spec.Spec.ServiceAccount.Name, "vso-"+spec.Spec.Namespace)
		if hasKindName(objects, "ServiceAccount", serviceAccount) {
			d.ok(fmt.Sprintf("ServiceAccount exists for app values: %s", serviceAccount))
		} else {
			d.fail(fmt.Sprintf("ServiceAccount %s not found in layer %s", serviceAccount, layer))
		}
	}

	d.checkVaultControlPlane(spec, layerDir)
	d.checkHarborImage(spec)
	d.checkClusterCapacity(spec)

	cmName, err := readGeneratedConfigMapName(filepath.Join(generatedDir, "kustomization.yaml"))
	if err != nil {
		d.fail(fmt.Sprintf("cannot parse generated kustomization: %v", err))
	}
	hrName, err := readHelmReleaseValuesFromName(filepath.Join(generatedDir, "helmrelease.yaml"))
	if err != nil {
		d.fail(fmt.Sprintf("cannot parse generated helmrelease: %v", err))
	}
	if err == nil && cmName != "" && hrName != "" {
		if cmName == hrName {
			d.ok(fmt.Sprintf("HelmRelease valuesFrom matches generated ConfigMap: %s", cmName))
		} else {
			d.fail(fmt.Sprintf("HelmRelease valuesFrom mismatch: hr=%s, cm=%s", hrName, cmName))
		}
	}

	d.checkLayerKustomizeBuild(layer)
	return nil
}

func (d *doctorChecker) checkLayerKustomizeBuild(layer string) {
	layerDir := filepath.Join(d.repoRoot, "clusters", "homelab", layer)
	if _, err := os.Stat(filepath.Join(layerDir, "kustomization.yaml")); err != nil {
		d.fail(fmt.Sprintf("layer kustomization missing: %s", filepath.Join(layerDir, "kustomization.yaml")))
		return
	}
	cmd := exec.Command("kubectl", "kustomize", layerDir)
	if err := cmd.Run(); err != nil {
		d.fail(fmt.Sprintf("kubectl kustomize layer failed: %s", layer))
		return
	}
	d.ok(fmt.Sprintf("kubectl kustomize layer passed: %s", layer))
}

func gitStatusPorcelain(path string) (string, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--", path)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

func readKustomizationResources(path string) ([]string, error) {
	var doc struct {
		Resources []string `yaml:"resources"`
	}
	if err := decodeYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	return doc.Resources, nil
}

func readNamespaceInfo(path string) (name string, gatewayAccess string, err error) {
	var doc struct {
		Metadata struct {
			Name   string            `yaml:"name"`
			Labels map[string]string `yaml:"labels"`
		} `yaml:"metadata"`
	}
	if err := decodeYAMLFile(path, &doc); err != nil {
		return "", "", err
	}
	return doc.Metadata.Name, doc.Metadata.Labels["gateway-access"], nil
}

func readCertificateSANs(path string) ([]string, error) {
	var doc struct {
		Spec struct {
			DNSNames []string `yaml:"dnsNames"`
		} `yaml:"spec"`
	}
	if err := decodeYAMLFile(path, &doc); err != nil {
		return nil, err
	}
	return doc.Spec.DNSNames, nil
}

func readGeneratedConfigMapName(path string) (string, error) {
	var doc struct {
		ConfigMapGenerator []struct {
			Name string `yaml:"name"`
		} `yaml:"configMapGenerator"`
	}
	if err := decodeYAMLFile(path, &doc); err != nil {
		return "", err
	}
	if len(doc.ConfigMapGenerator) == 0 {
		return "", nil
	}
	return doc.ConfigMapGenerator[0].Name, nil
}

func readHelmReleaseValuesFromName(path string) (string, error) {
	var doc struct {
		Spec struct {
			ValuesFrom []struct {
				Name string `yaml:"name"`
			} `yaml:"valuesFrom"`
		} `yaml:"spec"`
	}
	if err := decodeYAMLFile(path, &doc); err != nil {
		return "", err
	}
	if len(doc.Spec.ValuesFrom) == 0 {
		return "", nil
	}
	return doc.Spec.ValuesFrom[0].Name, nil
}

type kubeObjectRef struct {
	Kind string
	Name string
}

func scanKubeObjects(root string) ([]kubeObjectRef, error) {
	var refs []kubeObjectRef
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		base := strings.ToLower(filepath.Base(path))
		if !(strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml")) {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		dec := yaml.NewDecoder(file)
		for {
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			err = dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("decode %s: %w", path, err)
			}
			if doc.Kind == "" || doc.Metadata.Name == "" {
				continue
			}
			refs = append(refs, kubeObjectRef{Kind: doc.Kind, Name: doc.Metadata.Name})
		}
		return nil
	})
	return refs, err
}

func hasKindName(refs []kubeObjectRef, kind, name string) bool {
	for _, ref := range refs {
		if ref.Kind == kind && ref.Name == name {
			return true
		}
	}
	return false
}

func decodeYAMLFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, out)
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	return val
}

func envBool(key string) bool {
	val := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch val {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
