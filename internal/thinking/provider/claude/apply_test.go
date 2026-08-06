package claude

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/tidwall/gjson"
)

// aliasFable5ModelInfo mirrors an oauth-model-alias clone: alias ID with
// canonical capabilities.
func aliasFable5ModelInfo() *registry.ModelInfo {
	info := fable5ModelInfo()
	info.ID = "native-claude-fable-5"
	return info
}

func fable5ModelInfo() *registry.ModelInfo {
	return &registry.ModelInfo{
		ID:   "claude-fable-5",
		Type: "claude",
		Thinking: &registry.ThinkingSupport{
			Min:         1024,
			Max:         128000,
			ZeroAllowed: true,
			Levels:      []string{"low", "medium", "high", "xhigh", "max"},
		},
	}
}

func opus48ModelInfo() *registry.ModelInfo {
	return &registry.ModelInfo{
		ID:   "claude-opus-4-8",
		Type: "claude",
		Thinking: &registry.ThinkingSupport{
			Min:         1024,
			Max:         128000,
			ZeroAllowed: true,
			Levels:      []string{"low", "medium", "high", "xhigh", "max"},
		},
	}
}

// Budget-mode thinking on adaptive-only Claude 5 models must convert to
// adaptive with a summarized display: upstream returns thinking blocks with an
// empty thinking field for enabled+budget_tokens on these models.
func TestApplyBudgetConvertsToAdaptiveForAdaptiveOnlyModels(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"claude-fable-5","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":3000}}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeBudget, Budget: 3000}

	out, err := applier.Apply(body, config, fable5ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive, body=%s", got, out)
	}
	if gjson.GetBytes(out, "thinking.budget_tokens").Exists() {
		t.Fatalf("budget_tokens should be removed, body=%s", out)
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "medium" {
		t.Fatalf("effort = %q, want medium (budget 3000), body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.display").String(); got != "summarized" {
		t.Fatalf("thinking.display = %q, want summarized, body=%s", got, out)
	}
}

// Alias registry entries (oauth-model-alias, e.g. native-claude-fable-5) carry
// the alias as ModelInfo.ID and must convert exactly like the canonical ID.
func TestApplyBudgetConvertsForAliasModelID(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"native-claude-fable-5","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":3000}}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeBudget, Budget: 3000}

	out, err := applier.Apply(body, config, aliasFable5ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive, body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.display").String(); got != "summarized" {
		t.Fatalf("thinking.display = %q, want summarized, body=%s", got, out)
	}
}

// A caller-chosen display value survives the budget conversion.
func TestApplyBudgetConversionKeepsExplicitDisplay(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"claude-fable-5","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":30000,"display":"omitted"}}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeBudget, Budget: 30000}

	out, err := applier.Apply(body, config, fable5ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive, body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "output_config.effort").String(); got != "xhigh" {
		t.Fatalf("effort = %q, want xhigh (budget 30000), body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.display").String(); got != "omitted" {
		t.Fatalf("thinking.display = %q, want omitted preserved, body=%s", got, out)
	}
}

// Hybrid models that render budget thinking normally (Opus 4.8) keep the
// manual enabled+budget_tokens shape.
func TestApplyBudgetStaysManualForBudgetCapableModels(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"claude-opus-4-8","max_tokens":16000,"thinking":{"type":"enabled","budget_tokens":3000}}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeBudget, Budget: 3000}

	out, err := applier.Apply(body, config, opus48ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "enabled" {
		t.Fatalf("thinking.type = %q, want enabled, body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.budget_tokens").Int(); got != 3000 {
		t.Fatalf("budget_tokens = %d, want 3000, body=%s", got, out)
	}
}

// Adaptive level requests on omitted-by-default models gain display=summarized.
func TestApplyLevelAddsSummarizedDisplay(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"claude-fable-5","max_tokens":16000}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeLevel, Level: thinking.LevelHigh}

	out, err := applier.Apply(body, config, fable5ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive, body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.display").String(); got != "summarized" {
		t.Fatalf("thinking.display = %q, want summarized, body=%s", got, out)
	}
}

// Auto mode on adaptive models likewise defaults display for omitted-by-default models.
func TestApplyAutoAddsSummarizedDisplay(t *testing.T) {
	applier := NewApplier()
	body := []byte(`{"model":"claude-fable-5","max_tokens":16000}`)
	config := thinking.ThinkingConfig{Mode: thinking.ModeAuto, Budget: -1}

	out, err := applier.Apply(body, config, fable5ModelInfo())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := gjson.GetBytes(out, "thinking.type").String(); got != "adaptive" {
		t.Fatalf("thinking.type = %q, want adaptive, body=%s", got, out)
	}
	if got := gjson.GetBytes(out, "thinking.display").String(); got != "summarized" {
		t.Fatalf("thinking.display = %q, want summarized, body=%s", got, out)
	}
}

// clampAdaptiveLevel keeps supported levels, floors minimal, and caps unknown highs.
func TestClampAdaptiveLevel(t *testing.T) {
	info := fable5ModelInfo()
	if got := clampAdaptiveLevel("medium", info); got != "medium" {
		t.Fatalf("medium -> %q, want medium", got)
	}
	if got := clampAdaptiveLevel("minimal", info); got != "low" {
		t.Fatalf("minimal -> %q, want low", got)
	}
	if got := clampAdaptiveLevel("ultra", info); got != "max" {
		t.Fatalf("ultra -> %q, want max", got)
	}
}
