package appspec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	dns1123Re = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	layerRe   = regexp.MustCompile(`^[0-9]{2}-[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	digestRe  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	pathRe    = regexp.MustCompile(`clusters/homelab/([^/]+)/apps/([^/]+)/app\.yaml$`)
)

func Validate(spec *ServiceApp, sourcePath string) []error {
	var issues []error

	if spec.APIVersion != "platform.adminwg.dad/v1alpha1" {
		issues = append(issues, fmt.Errorf("apiVersion must be platform.adminwg.dad/v1alpha1"))
	}
	if spec.Kind != "ServiceApp" {
		issues = append(issues, fmt.Errorf("kind must be ServiceApp"))
	}

	if !dns1123Re.MatchString(spec.Metadata.Name) {
		issues = append(issues, fmt.Errorf("metadata.name must match DNS-1123 label"))
	}
	if !layerRe.MatchString(spec.Spec.Layer) {
		issues = append(issues, fmt.Errorf("spec.layer must match pattern NN-name (example: 11-rag)"))
	}
	if !dns1123Re.MatchString(spec.Spec.Namespace) {
		issues = append(issues, fmt.Errorf("spec.namespace must match DNS-1123 label"))
	}
	if spec.Spec.Tier != "" && spec.Spec.Tier != "dev" && spec.Spec.Tier != "stage" && spec.Spec.Tier != "prod" {
		issues = append(issues, fmt.Errorf("spec.tier must be one of: dev, stage, prod"))
	}

	if spec.Spec.ReplicaCount < 0 {
		issues = append(issues, fmt.Errorf("spec.replicaCount cannot be negative"))
	}

	if strings.TrimSpace(spec.Spec.Image.Repository) == "" {
		issues = append(issues, fmt.Errorf("spec.image.repository is required"))
	}
	if strings.TrimSpace(spec.Spec.Image.Tag) == "" && strings.TrimSpace(spec.Spec.Image.Digest) == "" {
		issues = append(issues, fmt.Errorf("spec.image.tag or spec.image.digest is required"))
	}
	if strings.EqualFold(strings.TrimSpace(spec.Spec.Image.Tag), "latest") {
		issues = append(issues, fmt.Errorf("spec.image.tag=latest is not allowed"))
	}
	if spec.Spec.Image.Digest != "" && !digestRe.MatchString(spec.Spec.Image.Digest) {
		issues = append(issues, fmt.Errorf("spec.image.digest must match sha256:<64 hex>"))
	}
	if spec.Spec.Tier == "prod" && strings.TrimSpace(spec.Spec.Image.Digest) == "" {
		issues = append(issues, fmt.Errorf("spec.tier=prod requires spec.image.digest (mutable tags are not allowed)"))
	}
	if spec.Spec.Image.PullPolicy != "" &&
		spec.Spec.Image.PullPolicy != "Always" &&
		spec.Spec.Image.PullPolicy != "IfNotPresent" &&
		spec.Spec.Image.PullPolicy != "Never" {
		issues = append(issues, fmt.Errorf("spec.image.pullPolicy must be one of: Always, IfNotPresent, Never"))
	}

	if spec.Spec.Service.Port < 1 || spec.Spec.Service.Port > 65535 {
		issues = append(issues, fmt.Errorf("spec.service.port must be 1..65535"))
	}
	if spec.Spec.ContainerPort != 0 && (spec.Spec.ContainerPort < 1 || spec.Spec.ContainerPort > 65535) {
		issues = append(issues, fmt.Errorf("spec.containerPort must be 1..65535"))
	}
	if spec.Spec.Service.Type != "" &&
		spec.Spec.Service.Type != "ClusterIP" &&
		spec.Spec.Service.Type != "NodePort" &&
		spec.Spec.Service.Type != "LoadBalancer" {
		issues = append(issues, fmt.Errorf("spec.service.type must be one of: ClusterIP, NodePort, LoadBalancer"))
	}

	if spec.Spec.Service.TargetPort != nil {
		switch v := spec.Spec.Service.TargetPort.(type) {
		case int:
			if v < 1 || v > 65535 {
				issues = append(issues, fmt.Errorf("spec.service.targetPort (int) must be 1..65535"))
			}
		case string:
			if strings.TrimSpace(v) == "" {
				issues = append(issues, fmt.Errorf("spec.service.targetPort (string) cannot be empty"))
			}
		default:
			issues = append(issues, fmt.Errorf("spec.service.targetPort must be int or string"))
		}
	}

	if spec.Spec.Resources.Profile != "" &&
		spec.Spec.Resources.Profile != "tiny" &&
		spec.Spec.Resources.Profile != "small" &&
		spec.Spec.Resources.Profile != "medium" &&
		spec.Spec.Resources.Profile != "large" {
		issues = append(issues, fmt.Errorf("spec.resources.profile must be one of: tiny, small, medium, large"))
	}
	if strings.TrimSpace(spec.Spec.Health.Path) == "" {
		issues = append(issues, fmt.Errorf("spec.health.path is required"))
	}

	if isEnabled(spec.Spec.Route.Enabled) && strings.TrimSpace(spec.Spec.Route.Host) == "" {
		issues = append(issues, fmt.Errorf("spec.route.host is required when spec.route.enabled=true"))
	}

	if spec.Spec.ImageAutomation.Order != "" &&
		spec.Spec.ImageAutomation.Order != "asc" &&
		spec.Spec.ImageAutomation.Order != "desc" {
		issues = append(issues, fmt.Errorf("spec.imageAutomation.order must be one of: asc, desc"))
	}

	issues = append(issues, validatePathConsistency(spec, sourcePath)...)
	return issues
}

func validatePathConsistency(spec *ServiceApp, sourcePath string) []error {
	normalized := filepath.ToSlash(sourcePath)
	matches := pathRe.FindStringSubmatch(normalized)
	if len(matches) != 3 {
		return nil
	}

	layerFromPath := matches[1]
	nameFromPath := matches[2]
	var issues []error

	if spec.Spec.Layer != layerFromPath {
		issues = append(issues, fmt.Errorf("spec.layer (%s) must match path layer (%s)", spec.Spec.Layer, layerFromPath))
	}
	if spec.Metadata.Name != nameFromPath {
		issues = append(issues, fmt.Errorf("metadata.name (%s) must match path app name (%s)", spec.Metadata.Name, nameFromPath))
	}
	return issues
}

func isEnabled(value *bool) bool {
	return value != nil && *value
}
