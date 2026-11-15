package config_test

import (
	"testing"

	"renstra/internal/config"
)

func TestApprovalConfigModes(t *testing.T) {
	// 1. Auto mode: all tools allowed without prompt
	auto := config.ApprovalConfig{Mode: "auto"}
	if decided, allowed := auto.Check("terminal", []byte(`{"cmd":"rm -rf /"}`)); !decided || !allowed {
		t.Fatalf("auto mode harus allow, dapat decided=%v, allowed=%v", decided, allowed)
	}

	// 2. Deny mode: all tools denied without prompt
	deny := config.ApprovalConfig{Mode: "deny"}
	if decided, allowed := deny.Check("terminal", []byte(`{"cmd":"ls"}`)); !decided || allowed {
		t.Fatalf("deny mode harus deny, dapat decided=%v, allowed=%v", decided, allowed)
	}

	// 3. Prompt mode (default): undecidable, needs user prompt
	prompt := config.ApprovalConfig{Mode: "prompt"}
	if decided, _ := prompt.Check("terminal", []byte(`{"cmd":"ls"}`)); decided {
		t.Fatal("prompt mode harus undecidable (tanya user)")
	}
}

func TestApprovalConfigGranularRules(t *testing.T) {
	cfg := config.ApprovalConfig{
		Mode: "prompt",
		AlwaysAllowed: []string{
			"terminal:git status",
			"write_file",
		},
		AlwaysDenied: []string{
			"terminal:rm -rf",
		},
	}

	// Always allowed rule by tool name
	if decided, allowed := cfg.Check("write_file", []byte(`{"path":"a.txt"}`)); !decided || !allowed {
		t.Fatal("write_file harus always allowed")
	}

	// Always allowed rule by tool:substring
	if decided, allowed := cfg.Check("terminal", []byte(`{"cmd":"git status -s"}`)); !decided || !allowed {
		t.Fatal("git status harus always allowed")
	}

	// Always denied rule by tool:substring
	if decided, allowed := cfg.Check("terminal", []byte(`{"cmd":"rm -rf /tmp/test"}`)); !decided || allowed {
		t.Fatal("rm -rf harus always denied")
	}

	// Neutral command: falls back to prompt
	if decided, _ := cfg.Check("terminal", []byte(`{"cmd":"cargo build"}`)); decided {
		t.Fatal("cargo build harus minta persetujuan user")
	}
}

func TestToolsConfigSwitches(t *testing.T) {
	no := false
	yes := true
	tools := config.ToolsConfig{
		Terminal: &no,
		Memory:   &yes,
	}

	if tools.IsEnabled("terminal") {
		t.Fatal("terminal harus mati")
	}
	if !tools.IsEnabled("memory") {
		t.Fatal("memory harus hidup")
	}
	if !tools.IsEnabled("read_file") {
		t.Fatal("tool yang tidak dispesifikasikan harus hidup secara default")
	}
}
