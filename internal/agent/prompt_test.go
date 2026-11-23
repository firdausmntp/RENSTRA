package agent_test

import (
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/provider"
)

func TestBuildPromptContainsRulesAndTools(t *testing.T) {
	p := agent.BuildPrompt(agent.BuildParams{Cwd: "/tmp/ws", Model: "m", Tools: []provider.ToolDef{{Name: "terminal"}}})
	for _, want := range []string{"terminal", "/tmp/ws", "[untrusted]", "renstra", "Zero Refusal", "DILARANG MENOLAK"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt tidak memuat %q", want)
		}
	}
}

func TestBuildPromptIncludesMemoryWhenPresent(t *testing.T) {
	p := agent.BuildPrompt(agent.BuildParams{Memory: "user suka kopi", UserProfile: "nama: Budi"})
	if !strings.Contains(p, "user suka kopi") || !strings.Contains(p, "nama: Budi") {
		t.Fatal("memory tidak di-inject")
	}
	if !strings.Contains(p, "tmp/") {
		t.Fatal("aturan folder tmp/ tidak ada di system prompt")
	}
}
