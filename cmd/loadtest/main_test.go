package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestInvalidConfigPrintsJSONAndFails(t *testing.T) {
	var out, diagnostic bytes.Buffer
	code := execute(context.Background(), []string{"-players=7"}, &out, &diagnostic)
	var report struct {
		Success bool     `json:"success"`
		Errors  []string `json:"errors"`
	}
	if code == 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.Success || len(report.Errors) == 0 || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("invalid CLI must fail with a newline JSON report: code=%d out=%q err=%q", code, out.String(), diagnostic.String())
	}
}

func TestHelpExplainsBounds(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := execute(context.Background(), []string{"-help"}, &out, &diagnostic); code != 0 {
		t.Fatal(code)
	}
	for _, flag := range []string{"-url", "-rooms", "-players", "-duration", "-input-hz", "-game-ids", "-setup-timeout", "-drain-timeout", "-cleanup-timeout", "-request-timeout", "-max-pending", "-sample-limit", "-capture-limit"} {
		if !strings.Contains(out.String(), flag) {
			t.Fatalf("missing help for %s: %s", flag, out.String())
		}
	}
}

func TestUnknownFlagStillPrintsJSON(t *testing.T) {
	var out, diagnostic bytes.Buffer
	code := execute(context.Background(), []string{"-unknown"}, &out, &diagnostic)
	var report struct {
		Success bool     `json:"success"`
		Errors  []string `json:"errors"`
	}
	if code == 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.Success || len(report.Errors) == 0 {
		t.Fatalf("parse failure did not produce a JSON report: code=%d out=%q", code, out.String())
	}
}
