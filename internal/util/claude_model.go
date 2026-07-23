package util

import "strings"

// IsClaudeThinkingModel checks if the model is a Claude thinking model
// that requires the interleaved-thinking beta header.
func IsClaudeThinkingModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.Contains(lower, "claude") && strings.Contains(lower, "thinking")
}

// ClaudeThinkingDisplayOmittedByDefault reports models whose thinking
// `display` defaults to "omitted" per the Anthropic thinking contract
// (docs/en/build-with-claude/thinking, verified 2026-07-23). Manual and
// adaptive thinking on these models must not request summaries unless the
// caller explicitly sets display: "summarized".
func ClaudeThinkingDisplayOmittedByDefault(model string) bool {
	switch model {
	case "claude-fable-5", "claude-mythos-5", "claude-mythos-preview",
		"claude-sonnet-5", "claude-opus-4-8", "claude-opus-4-7":
		return true
	}
	return false
}

const claudeDDModelPrefix = "claude-fable-5-dd-"

// EnsureClaudeModelIDPrefix rewrites model IDs for Anthropic /models listings.
// IDs that already start with "claude-" are returned unchanged; all other IDs
// become "claude-fable-5-dd-" plus the original ID with its characters reversed.
func EnsureClaudeModelIDPrefix(id string) string {
	if id == "" {
		return id
	}
	if strings.HasPrefix(id, "claude-") {
		return id
	}
	return claudeDDModelPrefix + reverseModelID(id)
}

// ResolveClaudeModelIDPrefix reverses EnsureClaudeModelIDPrefix for request routing.
// IDs that start with "claude-fable-5-dd-" are decoded by stripping the prefix and reversing
// the remainder. Optional thinking suffixes in model(value) form are preserved.
func ResolveClaudeModelIDPrefix(id string) string {
	if id == "" {
		return id
	}
	base, suffix, hasSuffix := splitModelThinkingSuffix(id)
	if !strings.HasPrefix(base, claudeDDModelPrefix) {
		return id
	}
	encoded := base[len(claudeDDModelPrefix):]
	if encoded == "" {
		return id
	}
	resolved := reverseModelID(encoded)
	if hasSuffix {
		return resolved + "(" + suffix + ")"
	}
	return resolved
}

// CanonicalClaudeModelID removes CLIProxyAPI's documented routing and thinking
// suffixes without treating prefix-sharing custom aliases as built-in models.
func CanonicalClaudeModelID(id string) string {
	resolved := ResolveClaudeModelIDPrefix(strings.TrimSpace(id))
	base, _, _ := splitModelThinkingSuffix(resolved)
	base = strings.ToLower(strings.TrimSpace(base))
	return strings.TrimSuffix(base, "[1m]")
}

func splitModelThinkingSuffix(model string) (base, suffix string, hasSuffix bool) {
	lastOpen := strings.LastIndex(model, "(")
	if lastOpen == -1 || !strings.HasSuffix(model, ")") {
		return model, "", false
	}
	return model[:lastOpen], model[lastOpen+1 : len(model)-1], true
}

func reverseModelID(id string) string {
	runes := []rune(id)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}
