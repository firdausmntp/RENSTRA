package config

import (
	"encoding/json"
	"strings"
)

// Check evaluates whether a dangerous tool execution is pre-approved,
// pre-denied, or needs to prompt the user. Returns (decided, allowed).
func (a ApprovalConfig) Check(toolName string, rawArgs []byte) (decided bool, allowed bool) {
	mode := strings.ToLower(strings.TrimSpace(a.Mode))
	if mode == "auto" || mode == "allow_all" || mode == "yes" {
		return true, true
	}
	if mode == "deny" || mode == "deny_all" || mode == "no" {
		return true, false
	}

	argStr := string(rawArgs)

	// Check always_denied rules first (aggressive substring match)
	for _, rule := range a.AlwaysDenied {
		if matchDenyRule(rule, toolName, argStr) {
			return true, false
		}
	}

	// Check always_allowed rules next (strict prefix/JSON-aware match)
	for _, rule := range a.AlwaysAllowed {
		if matchAllowRule(rule, toolName, rawArgs) {
			return true, true
		}
	}

	// Otherwise, let the approver prompt
	return false, false
}

func matchDenyRule(rule, toolName, argStr string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if rule == toolName || rule == "*" {
		return true
	}
	prefix := toolName + ":"
	if strings.HasPrefix(rule, prefix) {
		sub := strings.TrimPrefix(rule, prefix)
		return strings.Contains(argStr, sub)
	}
	return false
}

func matchAllowRule(rule, toolName string, rawArgs []byte) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if rule == toolName || rule == "*" {
		return true
	}
	prefix := toolName + ":"
	if !strings.HasPrefix(rule, prefix) {
		return false
	}
	sub := strings.TrimSpace(strings.TrimPrefix(rule, prefix))
	if sub == "" {
		return false
	}
	if sub == "*" {
		return true
	}

	var parsed map[string]any
	if err := json.Unmarshal(rawArgs, &parsed); err != nil {
		return false
	}

	if toolName == "terminal" {
		cmd, _ := parsed["cmd"].(string)
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return false
		}
		if hasShellMeta(cmd) {
			return false
		}
		if strings.HasSuffix(sub, "*") {
			base := strings.TrimSpace(strings.TrimSuffix(sub, "*"))
			return base == "" || cmd == base || strings.HasPrefix(cmd, base+" ")
		}
		return cmd == sub || strings.HasPrefix(cmd, sub+" ")
	}

	for _, v := range parsed {
		if sv, ok := v.(string); ok {
			sv = strings.TrimSpace(sv)
			if sv == sub || strings.HasPrefix(sv, sub+" ") || strings.HasPrefix(sv, sub) {
				return true
			}
		}
	}
	return false
}

func hasShellMeta(cmd string) bool {
	for _, tok := range []string{"&&", "||", ";", "|", "`", "$(", ">", "<", "\n", "\r", "#"} {
		if strings.Contains(cmd, tok) {
			return true
		}
	}
	return false
}
