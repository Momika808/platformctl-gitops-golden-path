package main

import (
	"strings"
	"testing"
)

func TestRewriteForPublic(t *testing.T) {
	input := `module gitlab.adminwg.dad/cluster/k8s/tools/platformctl
import "gitlab.adminwg.dad/cluster/k8s/tools/platformctl/internal/appspec"
# /Users/robot/Documents/Claster/k8s`

	out := rewriteForPublic(input, "github.com/acme/platformctl")
	if out == input {
		t.Fatalf("expected rewritten content")
	}
	if strings.Contains(out, "gitlab.adminwg.dad/cluster/k8s/tools/platformctl") {
		t.Fatalf("internal module path must be rewritten: %s", out)
	}
	if !strings.Contains(out, "github.com/acme/platformctl") {
		t.Fatalf("public module path missing: %s", out)
	}
	if strings.Contains(out, "/Users/robot/Documents/Claster/k8s") {
		t.Fatalf("absolute internal path should be rewritten: %s", out)
	}
	if !strings.Contains(out, "<repo-root>") {
		t.Fatalf("expected sanitized repo-root marker: %s", out)
	}
	if strings.Contains(out, "gitlab.adminwg.dad") {
		t.Fatalf("internal git host should be rewritten: %s", out)
	}
}
