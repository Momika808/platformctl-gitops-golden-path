package appspec

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ServiceApp struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`
}

type Metadata struct {
	Name string `yaml:"name"`
}

type Spec struct {
	Layer           string          `yaml:"layer"`
	Namespace       string          `yaml:"namespace"`
	Tier            string          `yaml:"tier,omitempty"`
	ReplicaCount    int             `yaml:"replicaCount,omitempty"`
	Image           ImageSpec       `yaml:"image"`
	Service         ServiceSpec     `yaml:"service"`
	ContainerPort   int             `yaml:"containerPort,omitempty"`
	Env             map[string]any  `yaml:"env,omitempty"`
	SecretEnv       []SecretEnvItem `yaml:"secretEnv,omitempty"`
	ServiceAccount  ServiceAccount  `yaml:"serviceAccount,omitempty"`
	ImagePullSecret string          `yaml:"imagePullSecret,omitempty"`
	Resources       ResourceSpec    `yaml:"resources,omitempty"`
	Health          HealthSpec      `yaml:"health,omitempty"`
	Monitoring      MonitoringSpec  `yaml:"monitoring,omitempty"`
	Route           RouteSpec       `yaml:"route,omitempty"`
	ImageAutomation ImageAutomation `yaml:"imageAutomation,omitempty"`
}

type ImageSpec struct {
	Repository string `yaml:"repository"`
	Tag        string `yaml:"tag,omitempty"`
	Digest     string `yaml:"digest,omitempty"`
	PullPolicy string `yaml:"pullPolicy,omitempty"`
}

type ServiceSpec struct {
	Port       int    `yaml:"port"`
	TargetPort any    `yaml:"targetPort,omitempty"`
	Type       string `yaml:"type,omitempty"`
}

type SecretEnvItem struct {
	Name       string `yaml:"name"`
	SecretName string `yaml:"secretName"`
	Key        string `yaml:"key"`
	Optional   *bool  `yaml:"optional,omitempty"`
}

type ServiceAccount struct {
	Name string `yaml:"name,omitempty"`
}

type ResourceSpec struct {
	Profile string `yaml:"profile,omitempty"`
}

type HealthSpec struct {
	Path string `yaml:"path,omitempty"`
}

type MonitoringSpec struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Path    string `yaml:"path,omitempty"`
}

type RouteSpec struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Host    string `yaml:"host,omitempty"`
	Path    string `yaml:"path,omitempty"`
	Profile string `yaml:"profile,omitempty"`
}

type ImageAutomation struct {
	Enabled                 *bool  `yaml:"enabled,omitempty"`
	TagPattern              string `yaml:"tagPattern,omitempty"`
	Order                   string `yaml:"order,omitempty"`
	GitSource               string `yaml:"gitSource,omitempty"`
	GitNamespace            string `yaml:"gitNamespace,omitempty"`
	ImageRepositoryInterval string `yaml:"imageRepositoryInterval,omitempty"`
	ImagePolicyInterval     string `yaml:"imagePolicyInterval,omitempty"`
	ImageAutomationInterval string `yaml:"imageAutomationInterval,omitempty"`
	HarborCASecret          string `yaml:"harborCASecret,omitempty"`
	RegistrySecret          string `yaml:"registrySecret,omitempty"`
}

func Load(path string) (*ServiceApp, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open app spec: %w", err)
	}
	defer file.Close()

	dec := yaml.NewDecoder(file)
	dec.KnownFields(true)

	var spec ServiceApp
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("decode yaml: %w", err)
	}

	// Prevent hidden second YAML document.
	var tail any
	if err := dec.Decode(&tail); err != nil && err != io.EOF {
		return nil, fmt.Errorf("decode trailing document: %w", err)
	}
	if tail != nil {
		return nil, fmt.Errorf("multiple YAML documents are not supported")
	}

	trimStringFields(&spec)
	return &spec, nil
}

func trimStringFields(spec *ServiceApp) {
	spec.APIVersion = strings.TrimSpace(spec.APIVersion)
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.Metadata.Name = strings.TrimSpace(spec.Metadata.Name)

	spec.Spec.Layer = strings.TrimSpace(spec.Spec.Layer)
	spec.Spec.Namespace = strings.TrimSpace(spec.Spec.Namespace)
	spec.Spec.Tier = strings.TrimSpace(spec.Spec.Tier)
	spec.Spec.Image.Repository = strings.TrimSpace(spec.Spec.Image.Repository)
	spec.Spec.Image.Tag = strings.TrimSpace(spec.Spec.Image.Tag)
	spec.Spec.Image.Digest = strings.TrimSpace(spec.Spec.Image.Digest)
	spec.Spec.Image.PullPolicy = strings.TrimSpace(spec.Spec.Image.PullPolicy)
	spec.Spec.Service.Type = strings.TrimSpace(spec.Spec.Service.Type)
	spec.Spec.ServiceAccount.Name = strings.TrimSpace(spec.Spec.ServiceAccount.Name)
	spec.Spec.ImagePullSecret = strings.TrimSpace(spec.Spec.ImagePullSecret)
	spec.Spec.Resources.Profile = strings.TrimSpace(spec.Spec.Resources.Profile)
	spec.Spec.Health.Path = strings.TrimSpace(spec.Spec.Health.Path)
	spec.Spec.Monitoring.Path = strings.TrimSpace(spec.Spec.Monitoring.Path)
	spec.Spec.Route.Host = strings.TrimSpace(spec.Spec.Route.Host)
	spec.Spec.Route.Path = strings.TrimSpace(spec.Spec.Route.Path)
	spec.Spec.Route.Profile = strings.TrimSpace(spec.Spec.Route.Profile)
	spec.Spec.ImageAutomation.TagPattern = strings.TrimSpace(spec.Spec.ImageAutomation.TagPattern)
	spec.Spec.ImageAutomation.Order = strings.TrimSpace(spec.Spec.ImageAutomation.Order)
	spec.Spec.ImageAutomation.GitSource = strings.TrimSpace(spec.Spec.ImageAutomation.GitSource)
	spec.Spec.ImageAutomation.GitNamespace = strings.TrimSpace(spec.Spec.ImageAutomation.GitNamespace)
	spec.Spec.ImageAutomation.ImageRepositoryInterval = strings.TrimSpace(spec.Spec.ImageAutomation.ImageRepositoryInterval)
	spec.Spec.ImageAutomation.ImagePolicyInterval = strings.TrimSpace(spec.Spec.ImageAutomation.ImagePolicyInterval)
	spec.Spec.ImageAutomation.ImageAutomationInterval = strings.TrimSpace(spec.Spec.ImageAutomation.ImageAutomationInterval)
	spec.Spec.ImageAutomation.HarborCASecret = strings.TrimSpace(spec.Spec.ImageAutomation.HarborCASecret)
	spec.Spec.ImageAutomation.RegistrySecret = strings.TrimSpace(spec.Spec.ImageAutomation.RegistrySecret)
}
