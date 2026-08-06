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
	switch CanonicalClaudeModelID(model) {
	case "claude-fable-5", "claude-mythos-5", "claude-mythos-preview",
		"claude-sonnet-5", "claude-opus-4-8", "claude-opus-4-7":
		return true
	}
	return false
}

// ClaudeThinkingDisplayOmittedForAlias extends the display-omitted check to
// registry alias IDs (oauth-model-alias) that embed a canonical model name,
// e.g. native-claude-fable-5.
func ClaudeThinkingDisplayOmittedForAlias(model string) bool {
	if ClaudeThinkingDisplayOmittedByDefault(model) {
		return true
	}
	lower := strings.ToLower(model)
	for _, canonical := range []string{
		"claude-fable-5", "claude-mythos-5", "claude-mythos-preview",
		"claude-sonnet-5", "claude-opus-4-8", "claude-opus-4-7",
	} {
		if strings.Contains(lower, canonical) {
			return true
		}
	}
	return false
}

// claudeAdaptiveOnlyThinkingModels are Claude 5-family models whose manual
// budget thinking is accepted upstream but always returns an empty thinking
// field, even with display: "summarized" (verified against api.anthropic.com
// on claude-fable-5, 2026-08-06: enabled+budget_tokens bills thinking tokens
// yet returns "" with a signature; adaptive+display returns text). Budget
// requests for these models must be converted to adaptive thinking for the
// content to be visible.
var claudeAdaptiveOnlyThinkingModels = []string{
	"claude-fable-5", "claude-mythos-5", "claude-mythos-preview",
	"claude-sonnet-5", "claude-opus-5",
}

// ClaudeAdaptiveOnlyThinkingModel reports whether the model ID names an
// adaptive-only Claude 5 thinking model. Substring matching is deliberate:
// oauth-model-alias clones a registry entry under an arbitrary alias ID
// (e.g. native-claude-fable-5) while keeping canonical capabilities, and
// suffixed forms such as claude-fable-5[1m] must also convert.
func ClaudeAdaptiveOnlyThinkingModel(model string) bool {
	lower := strings.ToLower(model)
	for _, canonical := range claudeAdaptiveOnlyThinkingModels {
		if strings.Contains(lower, canonical) {
			return true
		}
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
