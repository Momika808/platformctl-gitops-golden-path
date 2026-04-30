package appspec

import "testing"

func TestValidate_ValidSpec(t *testing.T) {
	spec := &ServiceApp{
		APIVersion: "platform.adminwg.dad/v1alpha1",
		Kind:       "ServiceApp",
		Metadata: Metadata{
			Name: "retrieval-api",
		},
		Spec: Spec{
			Layer:     "11-rag",
			Namespace: "rag",
			Image: ImageSpec{
				Repository: "harbor.home.arpa/dota2-assistant/nested-dynamics-retrieval-api",
				Tag:        "main",
				PullPolicy: "Always",
			},
			Service: ServiceSpec{
				Port: 8080,
			},
			Health: HealthSpec{
				Path: "/healthz",
			},
		},
	}

	issues := Validate(spec, "/tmp/clusters/homelab/11-rag/apps/retrieval-api/app.yaml")
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %d: %v", len(issues), issues)
	}
}

func TestValidate_InvalidSpec(t *testing.T) {
	spec := &ServiceApp{
		APIVersion: "wrong",
		Kind:       "wrong",
		Metadata: Metadata{
			Name: "Bad_Name",
		},
		Spec: Spec{
			Layer:     "rag",
			Namespace: "Rag",
			Image: ImageSpec{
				Repository: "",
				Digest:     "sha256:123",
				PullPolicy: "sometimes",
			},
			Service: ServiceSpec{
				Port: 0,
			},
			Route: RouteSpec{
				Enabled: boolPtr(true),
				Host:    "",
			},
			Health: HealthSpec{},
		},
	}

	issues := Validate(spec, "/tmp/clusters/homelab/11-rag/apps/retrieval-api/app.yaml")
	if len(issues) == 0 {
		t.Fatalf("expected validation issues, got none")
	}
}

func TestValidate_DisallowLatestTag(t *testing.T) {
	spec := &ServiceApp{
		APIVersion: "platform.adminwg.dad/v1alpha1",
		Kind:       "ServiceApp",
		Metadata:   Metadata{Name: "demo"},
		Spec: Spec{
			Layer:     "11-rag",
			Namespace: "rag",
			Image: ImageSpec{
				Repository: "harbor.home.arpa/example/demo",
				Tag:        "latest",
			},
			Service: ServiceSpec{Port: 8080},
			Health:  HealthSpec{Path: "/healthz"},
		},
	}

	issues := Validate(spec, "/tmp/clusters/homelab/11-rag/apps/demo/app.yaml")
	if len(issues) == 0 {
		t.Fatalf("expected issues for latest tag")
	}
}

func TestValidate_ProdRequiresDigest(t *testing.T) {
	spec := &ServiceApp{
		APIVersion: "platform.adminwg.dad/v1alpha1",
		Kind:       "ServiceApp",
		Metadata:   Metadata{Name: "demo"},
		Spec: Spec{
			Layer:     "11-rag",
			Namespace: "rag",
			Tier:      "prod",
			Image: ImageSpec{
				Repository: "harbor.home.arpa/example/demo",
				Tag:        "v1.2.3",
			},
			Service: ServiceSpec{Port: 8080},
			Health:  HealthSpec{Path: "/healthz"},
		},
	}

	issues := Validate(spec, "/tmp/clusters/homelab/11-rag/apps/demo/app.yaml")
	if len(issues) == 0 {
		t.Fatalf("expected issues for prod without digest")
	}
}

func boolPtr(v bool) *bool {
	return &v
}
