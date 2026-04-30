package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitForVaultRoleAndPolicy_SucceedsAfterRetry(t *testing.T) {
	t.Helper()

	roleCalls := 0
	policyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/auth/kubernetes/role/"):
			roleCalls++
			if roleCalls < 2 {
				http.Error(w, `{"errors":["not found"]}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"name":"vso-demo"}}`))
		case strings.HasPrefix(r.URL.Path, "/v1/sys/policies/acl/"):
			policyCalls++
			if policyCalls < 3 {
				http.Error(w, `{"errors":["not found"]}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"name":"vso-demo"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := newAppAutoConfig{
		VaultAddr:           server.URL,
		VaultToken:          "token-demo",
		VaultRole:           "vso-demo",
		VaultPolicy:         "vso-demo",
		VaultRequestTimeout: 2 * time.Second,
		VaultVerifyTimeout:  2 * time.Second,
		VaultVerifyPoll:     25 * time.Millisecond,
	}

	if err := waitForVaultRoleAndPolicy(cfg); err != nil {
		t.Fatalf("waitForVaultRoleAndPolicy: %v", err)
	}
	if roleCalls < 2 {
		t.Fatalf("expected role to be retried, calls=%d", roleCalls)
	}
	if policyCalls < 3 {
		t.Fatalf("expected policy to be retried, calls=%d", policyCalls)
	}
}

func TestWaitForVaultRoleAndPolicy_FailsFastOnForbidden(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":["permission denied"]}`, http.StatusForbidden)
	}))
	defer server.Close()

	cfg := newAppAutoConfig{
		VaultAddr:           server.URL,
		VaultToken:          "bad-token",
		VaultRole:           "vso-demo",
		VaultPolicy:         "vso-demo",
		VaultRequestTimeout: 2 * time.Second,
		VaultVerifyTimeout:  5 * time.Second,
		VaultVerifyPoll:     100 * time.Millisecond,
	}

	start := time.Now()
	err := waitForVaultRoleAndPolicy(cfg)
	if err == nil {
		t.Fatalf("expected forbidden error")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("expected HTTP 403 in error, got: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("expected fast failure on forbidden, took %s", time.Since(start))
	}
}

func TestMergeMergeRequestBySourceBranch_AutoRebaseOnConflict(t *testing.T) {
	t.Helper()

	mergeCalls := 0
	rebaseCalls := 0
	rebasePollCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/cluster/k8s/merge_requests":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":                    500,
					"iid":                   42,
					"state":                 "opened",
					"web_url":               "https://gitlab.local/cluster/k8s/-/merge_requests/42",
					"source_branch":         "feature/rebase-demo",
					"merge_status":          "cannot_be_merged_recheck",
					"detailed_merge_status": "checking",
					"has_conflicts":         true,
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v4/projects/cluster/k8s/merge_requests/42/merge":
			mergeCalls++
			if mergeCalls == 1 {
				http.Error(w, `{"message":"Branch cannot be merged"}`, http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      500,
				"iid":     42,
				"state":   "merged",
				"web_url": "https://gitlab.local/cluster/k8s/-/merge_requests/42",
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v4/projects/cluster/k8s/merge_requests/42/rebase":
			rebaseCalls++
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"rebase_in_progress":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v4/projects/cluster/k8s/merge_requests/42":
			rebasePollCalls++
			w.Header().Set("Content-Type", "application/json")
			if rebasePollCalls == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":                 500,
					"iid":                42,
					"state":              "opened",
					"web_url":            "https://gitlab.local/cluster/k8s/-/merge_requests/42",
					"rebase_in_progress": true,
					"merge_error":        nil,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                 500,
				"iid":                42,
				"state":              "opened",
				"web_url":            "https://gitlab.local/cluster/k8s/-/merge_requests/42",
				"rebase_in_progress": false,
				"merge_error":        nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := newGitLabClient(server.URL, "token-demo", 2*time.Second)
	if err != nil {
		t.Fatalf("newGitLabClient: %v", err)
	}

	mr, err := mergeMergeRequestBySourceBranch(client, "cluster/k8s", "feature/rebase-demo", true)
	if err != nil {
		t.Fatalf("mergeMergeRequestBySourceBranch: %v", err)
	}
	if mr == nil || mr.State != "merged" {
		t.Fatalf("expected merged merge request, got %#v", mr)
	}
	if mergeCalls != 2 {
		t.Fatalf("expected two merge attempts (before and after rebase), got %d", mergeCalls)
	}
	if rebaseCalls != 1 {
		t.Fatalf("expected one rebase call, got %d", rebaseCalls)
	}
	if rebasePollCalls < 1 {
		t.Fatalf("expected rebase polling calls, got %d", rebasePollCalls)
	}
}
