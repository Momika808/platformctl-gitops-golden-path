package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGitLabClient_ListMergeRequestsByProject(t *testing.T) {
	t.Helper()

	var gotPath string
	var gotSourceBranch string
	var gotState string
	var gotToken string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSourceBranch = r.URL.Query().Get("source_branch")
		gotState = r.URL.Query().Get("state")
		gotToken = r.Header.Get("PRIVATE-TOKEN")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":            100,
				"iid":           42,
				"state":         "opened",
				"web_url":       "https://gitlab.local/mr/42",
				"source_branch": "feature/demo",
			},
		})
	}))
	defer server.Close()

	client, err := newGitLabClient(server.URL, "token-1", 2*time.Second)
	if err != nil {
		t.Fatalf("newGitLabClient: %v", err)
	}

	mrs, err := client.listMergeRequestsByProject("cluster/k8s", "feature/demo", "all", 20)
	if err != nil {
		t.Fatalf("listMergeRequestsByProject: %v", err)
	}
	if len(mrs) != 1 {
		t.Fatalf("expected 1 merge request, got %d", len(mrs))
	}
	if mrs[0].IID != 42 {
		t.Fatalf("expected iid=42, got %d", mrs[0].IID)
	}
	if gotPath != "/api/v4/projects/cluster/k8s/merge_requests" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotSourceBranch != "feature/demo" {
		t.Fatalf("unexpected source_branch: %s", gotSourceBranch)
	}
	if gotState != "all" {
		t.Fatalf("unexpected state: %s", gotState)
	}
	if gotToken != "token-1" {
		t.Fatalf("unexpected token header: %s", gotToken)
	}
}

func TestGitLabClient_MergeMergeRequestByProject(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string
	var gotRemoveSource bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		v, _ := payload["should_remove_source_branch"].(bool)
		gotRemoveSource = v

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            200,
			"iid":           77,
			"state":         "merged",
			"web_url":       "https://gitlab.local/mr/77",
			"source_branch": "feature/demo",
		})
	}))
	defer server.Close()

	client, err := newGitLabClient(server.URL, "token-2", 2*time.Second)
	if err != nil {
		t.Fatalf("newGitLabClient: %v", err)
	}

	mr, err := client.mergeMergeRequestByProject("cluster/k8s", 77, true)
	if err != nil {
		t.Fatalf("mergeMergeRequestByProject: %v", err)
	}
	if mr.State != "merged" {
		t.Fatalf("expected state=merged, got %s", mr.State)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/api/v4/projects/cluster/k8s/merge_requests/77/merge" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if !gotRemoveSource {
		t.Fatalf("expected should_remove_source_branch=true")
	}
}

func TestGitLabClient_GetMergeRequestByProject(t *testing.T) {
	t.Helper()

	var gotPath string
	var gotIncludeRebase string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIncludeRebase = r.URL.Query().Get("include_rebase_in_progress")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                 321,
			"iid":                88,
			"state":              "opened",
			"web_url":            "https://gitlab.local/mr/88",
			"source_branch":      "feature/demo",
			"rebase_in_progress": false,
			"merge_error":        nil,
		})
	}))
	defer server.Close()

	client, err := newGitLabClient(server.URL, "token-3", 2*time.Second)
	if err != nil {
		t.Fatalf("newGitLabClient: %v", err)
	}

	mr, err := client.getMergeRequestByProject("cluster/k8s", 88, true)
	if err != nil {
		t.Fatalf("getMergeRequestByProject: %v", err)
	}
	if mr.IID != 88 {
		t.Fatalf("expected iid=88, got %d", mr.IID)
	}
	if gotPath != "/api/v4/projects/cluster/k8s/merge_requests/88" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotIncludeRebase != "true" {
		t.Fatalf("unexpected include_rebase_in_progress: %q", gotIncludeRebase)
	}
}

func TestGitLabClient_RebaseMergeRequestByProject(t *testing.T) {
	t.Helper()

	var gotMethod string
	var gotPath string
	var gotSkipCI string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotSkipCI = r.URL.Query().Get("skip_ci")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rebase_in_progress":true}`))
	}))
	defer server.Close()

	client, err := newGitLabClient(server.URL, "token-4", 2*time.Second)
	if err != nil {
		t.Fatalf("newGitLabClient: %v", err)
	}

	if err := client.rebaseMergeRequestByProject("cluster/k8s", 91, true); err != nil {
		t.Fatalf("rebaseMergeRequestByProject: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("unexpected method: %s", gotMethod)
	}
	if gotPath != "/api/v4/projects/cluster/k8s/merge_requests/91/rebase" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotSkipCI != "true" {
		t.Fatalf("expected skip_ci=true, got %q", gotSkipCI)
	}
}
