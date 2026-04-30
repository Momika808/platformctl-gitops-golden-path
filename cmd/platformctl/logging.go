package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func logInfo(msg string, fields map[string]any) {
	logEvent("info", msg, fields)
}

func logWarn(msg string, fields map[string]any) {
	logEvent("warn", msg, fields)
}

func logError(msg string, fields map[string]any) {
	logEvent("error", msg, fields)
}

func logEvent(level string, msg string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	record := map[string]any{
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"level": strings.ToLower(strings.TrimSpace(level)),
		"msg":   msg,
	}
	for k, v := range fields {
		record[k] = v
	}

	if isJSONLoggingEnabled() {
		raw, err := json.Marshal(record)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[WARN] log marshal failed: %v\n", err)
			return
		}
		fmt.Fprintln(os.Stderr, string(raw))
		return
	}

	parts := make([]string, 0, len(record)+1)
	parts = append(parts, fmt.Sprintf("[%s] %s", strings.ToUpper(record["level"].(string)), msg))
	for k, v := range record {
		if k == "level" || k == "msg" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	fmt.Fprintln(os.Stderr, strings.Join(parts, " "))
}

func emitAlertIfConfigured(title string, fields map[string]any) {
	webhook := strings.TrimSpace(os.Getenv("PLATFORMCTL_ALERT_WEBHOOK_URL"))
	if webhook == "" {
		return
	}
	body := map[string]any{
		"title": title,
		"ts":    time.Now().UTC().Format(time.RFC3339Nano),
		"data":  fields,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}

	req, err := http.NewRequest(http.MethodPost, webhook, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

func isJSONLoggingEnabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PLATFORMCTL_LOG_FORMAT")), "json") {
		return true
	}
	switch strings.TrimSpace(os.Getenv("PLATFORMCTL_LOG_JSON")) {
	case "1", "true", "TRUE", "yes", "YES":
		return true
	default:
		return false
	}
}
