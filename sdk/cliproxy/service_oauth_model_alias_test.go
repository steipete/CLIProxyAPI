package cliproxy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestApplyOAuthModelAlias_Rename(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", DisplayName: "Configured GPT Five"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "g5" {
		t.Fatalf("expected model id %q, got %q", "g5", out[0].ID)
	}
	if out[0].Name != "models/g5" {
		t.Fatalf("expected model name %q, got %q", "models/g5", out[0].Name)
	}
	if out[0].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected display name %q, got %q", "Configured GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_ForkAddsAlias(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", Fork: true, DisplayName: "Configured GPT Five"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 2 {
		t.Fatalf("expected 2 models, got %d", len(out))
	}
	if out[0].ID != "gpt-5" {
		t.Fatalf("expected first model id %q, got %q", "gpt-5", out[0].ID)
	}
	if out[1].ID != "g5" {
		t.Fatalf("expected second model id %q, got %q", "g5", out[1].ID)
	}
	if out[1].Name != "models/g5" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5", out[1].Name)
	}
	if out[0].DisplayName != "Upstream GPT Five" {
		t.Fatalf("expected original display name %q, got %q", "Upstream GPT Five", out[0].DisplayName)
	}
	if out[1].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected alias display name %q, got %q", "Configured GPT Five", out[1].DisplayName)
	}
}

func TestApplyOAuthModelAlias_PreservesUpstreamDisplayNameByDefault(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", DisplayName: "Upstream GPT Five"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].DisplayName != "Upstream GPT Five" {
		t.Fatalf("expected upstream display name %q, got %q", "Upstream GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_ForkAddsMultipleAliases(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"codex": {
				{Name: "gpt-5", Alias: "g5", Fork: true},
				{Name: "gpt-5", Alias: "g5-2", Fork: true},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "gpt-5", Name: "models/gpt-5"},
	}

	out := applyOAuthModelAlias(cfg, "codex", "oauth", models)
	if len(out) != 3 {
		t.Fatalf("expected 3 models, got %d", len(out))
	}
	if out[0].ID != "gpt-5" {
		t.Fatalf("expected first model id %q, got %q", "gpt-5", out[0].ID)
	}
	if out[1].ID != "g5" {
		t.Fatalf("expected second model id %q, got %q", "g5", out[1].ID)
	}
	if out[1].Name != "models/g5" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5", out[1].Name)
	}
	if out[2].ID != "g5-2" {
		t.Fatalf("expected third model id %q, got %q", "g5-2", out[2].ID)
	}
	if out[2].Name != "models/g5-2" {
		t.Fatalf("expected forked model name %q, got %q", "models/g5-2", out[2].Name)
	}
}

func TestApplyOAuthModelAlias_PluginProvider(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"sample-provider": {
				{Name: "sample-model-latest", Alias: "sample-latest"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "sample-model-latest", Name: "models/sample-model-latest"},
	}

	out := applyOAuthModelAlias(cfg, "sample-provider", "oauth", models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "sample-latest" {
		t.Fatalf("expected plugin alias id %q, got %q", "sample-latest", out[0].ID)
	}
	if out[0].Name != "models/sample-latest" {
		t.Fatalf("expected plugin alias name %q, got %q", "models/sample-latest", out[0].Name)
	}
}

func TestApplyOAuthModelAlias_PluginProviderSkipsAPIKey(t *testing.T) {
	cfg := &config.Config{
		OAuthModelAlias: map[string][]config.OAuthModelAlias{
			"sample-provider": {
				{Name: "sample-model-latest", Alias: "sample-latest"},
			},
		},
	}
	models := []*ModelInfo{
		{ID: "sample-model-latest", Name: "models/sample-model-latest"},
	}

	out := applyOAuthModelAlias(cfg, "sample-provider", "api_key", models)
	if len(out) != 1 || out[0].ID != "sample-model-latest" {
		t.Fatalf("expected API key plugin model to remain unchanged, got %#v", out)
	}
}

func TestApplyOAuthModelAlias_PerAuthAlias(t *testing.T) {
	models := []*ModelInfo{
		{ID: "gpt-5.3-codex-spark", Name: "models/gpt-5.3-codex-spark"},
	}
	attributes := map[string]string{
		"model_aliases": `[{"name":"gpt-5.3-codex-spark","alias":"gpt-5.5","display-name":"Configured GPT Five"}]`,
	}

	out := applyOAuthModelAliasForAuth(nil, "codex", "oauth", attributes, models)
	if len(out) != 1 {
		t.Fatalf("expected 1 model, got %d", len(out))
	}
	if out[0].ID != "gpt-5.5" {
		t.Fatalf("expected per-auth alias id %q, got %q", "gpt-5.5", out[0].ID)
	}
	if out[0].Name != "models/gpt-5.5" {
		t.Fatalf("expected per-auth alias name %q, got %q", "models/gpt-5.5", out[0].Name)
	}
	if out[0].DisplayName != "Configured GPT Five" {
		t.Fatalf("expected per-auth display name %q, got %q", "Configured GPT Five", out[0].DisplayName)
	}
}

func TestApplyOAuthModelAlias_HiddenSourceTemplate(t *testing.T) {
	const (
		hiddenModel = "hidden-codex-model"
		aliasModel  = "codex-latest"
		templateID  = "gpt-5.6-sol"
	)
	template := &ModelInfo{
		ID:                  templateID,
		Name:                "models/" + templateID,
		DisplayName:         "Visible Codex",
		ContextLength:       400000,
		MaxCompletionTokens: 128000,
		Thinking:            &registry.ThinkingSupport{Min: 1024, Max: 32768},
	}

	tests := []struct {
		name              string
		provider          string
		authKind          string
		aliases           []config.OAuthModelAlias
		models            []*ModelInfo
		wantAlias         bool
		wantExistingAlias bool
		wantDisplay       string
		wantContext       int
	}{
		{
			name:        "hidden source clones visible template",
			provider:    "codex",
			authKind:    "oauth",
			aliases:     []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID}},
			models:      []*ModelInfo{template},
			wantAlias:   true,
			wantDisplay: template.DisplayName,
			wantContext: template.ContextLength,
		},
		{
			name:        "hidden source respects display override and fork never exposes source",
			provider:    "codex",
			authKind:    "oauth",
			aliases:     []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID, Fork: true, DisplayName: "Latest Codex"}},
			models:      []*ModelInfo{template},
			wantAlias:   true,
			wantDisplay: "Latest Codex",
			wantContext: template.ContextLength,
		},
		{
			name:     "hidden source without template stays hidden",
			provider: "codex",
			authKind: "oauth",
			aliases:  []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel}},
			models:   []*ModelInfo{template},
		},
		{
			name:     "unknown template fails closed",
			provider: "codex",
			authKind: "oauth",
			aliases:  []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: "missing-template"}},
			models:   []*ModelInfo{template},
		},
		{
			name:     "provider mismatch fails closed",
			provider: "claude",
			authKind: "oauth",
			aliases:  []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID}},
			models:   []*ModelInfo{template},
		},
		{
			name:     "API key does not inherit OAuth aliases",
			provider: "codex",
			authKind: "api_key",
			aliases:  []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID}},
			models:   []*ModelInfo{template},
		},
		{
			name:              "catalogue alias collision does not replace existing model",
			provider:          "codex",
			authKind:          "oauth",
			aliases:           []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID}},
			models:            []*ModelInfo{template, {ID: aliasModel, ContextLength: 123}},
			wantExistingAlias: true,
			wantContext:       123,
		},
		{
			name:        "existing source remains authoritative",
			provider:    "codex",
			authKind:    "oauth",
			aliases:     []config.OAuthModelAlias{{Name: hiddenModel, Alias: aliasModel, Template: templateID}},
			models:      []*ModelInfo{template, {ID: hiddenModel, Name: "models/" + hiddenModel, DisplayName: "Actual Source", ContextLength: 999}},
			wantAlias:   true,
			wantDisplay: "Actual Source",
			wantContext: 999,
		},
		{
			name:     "configured alias collision keeps existing alias mapping",
			provider: "codex",
			authKind: "oauth",
			aliases: []config.OAuthModelAlias{
				{Name: templateID, Alias: aliasModel, Fork: true},
				{Name: hiddenModel, Alias: aliasModel, Template: templateID},
			},
			models:            []*ModelInfo{template},
			wantExistingAlias: true,
			wantContext:       template.ContextLength,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{"codex": testCase.aliases}}
			got, validated := applyOAuthModelAliasForAuthWithValidation(cfg, testCase.provider, testCase.authKind, nil, testCase.models)
			if testCase.wantAlias {
				if len(validated) != 1 || validated[0].Alias != aliasModel || validated[0].Name != hiddenModel {
					t.Fatalf("validated aliases = %+v, want the synthesized alias", validated)
				}
			} else if len(validated) != 0 {
				t.Fatalf("invalid hidden alias reached routing validation: %+v", validated)
			}
			var alias *ModelInfo
			for _, model := range got {
				if model.ID == hiddenModel {
					t.Fatalf("hidden upstream model leaked into catalogue: %+v", model)
				}
				if model.ID == aliasModel {
					alias = model
				}
			}
			if testCase.wantAlias {
				if alias == nil {
					t.Fatalf("missing alias %q in models: %+v", aliasModel, got)
				}
				if alias.DisplayName != testCase.wantDisplay || alias.ContextLength != testCase.wantContext {
					t.Fatalf("alias capabilities = %+v, want display %q and context %d", alias, testCase.wantDisplay, testCase.wantContext)
				}
				if alias.Name != "models/"+aliasModel {
					t.Fatalf("alias name = %q, want %q", alias.Name, "models/"+aliasModel)
				}
				return
			}
			if testCase.wantExistingAlias {
				if alias == nil || alias.ContextLength != testCase.wantContext {
					t.Fatalf("existing alias = %+v, want context %d", alias, testCase.wantContext)
				}
				return
			}
			if alias != nil {
				t.Fatalf("unexpected synthesized alias: %+v", alias)
			}
		})
	}
}

func TestRegisterModelsForAuth_HiddenOAuthAliasCredentialScope(t *testing.T) {
	const (
		hiddenModel = "hidden-codex-model"
		aliasModel  = "codex-latest"
		templateID  = "gpt-5.6-sol"
	)
	service := &Service{cfg: &config.Config{}}
	owner := &coreauth.Auth{
		ID:       "hidden-oauth-alias-owner",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			coreauth.AttributeAuthKind: "oauth",
			"model_aliases":            `[{"name":"hidden-codex-model","alias":"codex-latest","template":"gpt-5.6-sol","force-mapping":true}]`,
		},
	}
	other := &coreauth.Auth{
		ID:         "hidden-oauth-alias-other",
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{coreauth.AttributeAuthKind: "oauth"},
	}
	modelRegistry := registry.GetGlobalRegistry()
	t.Cleanup(func() {
		modelRegistry.UnregisterClient(owner.ID)
		modelRegistry.UnregisterClient(other.ID)
	})

	service.registerModelsForAuth(context.Background(), owner)
	service.registerModelsForAuth(context.Background(), other)
	if !modelRegistry.ClientSupportsModel(owner.ID, aliasModel) {
		t.Fatalf("alias owner does not support %q", aliasModel)
	}
	if modelRegistry.ClientSupportsModel(other.ID, aliasModel) {
		t.Fatalf("unrelated credential unexpectedly supports %q", aliasModel)
	}
	for _, authID := range []string{owner.ID, other.ID} {
		if modelRegistry.ClientSupportsModel(authID, hiddenModel) {
			t.Fatalf("credential %q exposes hidden source model", authID)
		}
	}
	listing, errMarshal := json.Marshal(modelRegistry.GetAvailableModels("openai"))
	if errMarshal != nil {
		t.Fatalf("marshal model listing: %v", errMarshal)
	}
	if !strings.Contains(string(listing), `"id":"`+aliasModel+`"`) || strings.Contains(string(listing), hiddenModel) {
		t.Fatalf("model discovery = %s, want alias without hidden source", listing)
	}
}

type hiddenAliasTestExecutor struct{ serviceTestPluginExecutor }

func (hiddenAliasTestExecutor) Identifier() string { return "codex" }

func TestServiceHotReload_HiddenOAuthAliasInvalidToValid(t *testing.T) {
	const (
		provider      = "codex"
		hiddenModel   = "hidden-codex-model"
		aliasModel    = "codex-latest"
		templateModel = "gpt-5.6-sol"
	)
	configForTemplate := func(template string) *config.Config {
		return &config.Config{OAuthModelAlias: map[string][]config.OAuthModelAlias{
			provider: {{Name: hiddenModel, Alias: aliasModel, Template: template, ForceMapping: true}},
		}}
	}
	invalidConfig := configForTemplate("missing-template")
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(hiddenAliasTestExecutor{})
	manager.SetOAuthModelAlias(invalidConfig.OAuthModelAlias)
	credential := &coreauth.Auth{
		ID:         "hidden-oauth-hot-reload",
		Provider:   provider,
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{coreauth.AttributeAuthKind: coreauth.AuthKindOAuth},
	}
	if _, errRegister := manager.Register(context.Background(), credential); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	modelRegistry := registry.GetGlobalRegistry()
	t.Cleanup(func() { modelRegistry.UnregisterClient(credential.ID) })
	service := &Service{cfg: invalidConfig, coreManager: manager}
	service.registerModelsForAuth(context.Background(), credential)
	manager.RefreshSchedulerEntry(credential.ID)

	assertInvalid := func() {
		t.Helper()
		if modelRegistry.ClientSupportsModel(credential.ID, aliasModel) {
			t.Fatal("invalid alias leaked into registered model discovery")
		}
		if _, errSelect := manager.SelectAuth(context.Background(), provider, aliasModel, cliproxyexecutor.Options{}); errSelect == nil {
			t.Fatal("invalid alias remained eligible after config reload")
		}
		if got := manager.ResolveExecutionModel(credential, aliasModel); got != aliasModel {
			t.Fatalf("invalid alias rewrote request to %q", got)
		}
	}
	assertInvalid()

	validConfig := configForTemplate(templateModel)
	service.applyWatcherConfigUpdate(validConfig)
	if !service.refreshModelRegistrationForAuth(credential) {
		t.Fatal("valid config reload did not refresh credential registration")
	}
	if !modelRegistry.ClientSupportsModel(credential.ID, aliasModel) {
		t.Fatal("valid alias was not registered after config reload")
	}
	if _, errSelect := manager.SelectAuth(context.Background(), provider, aliasModel, cliproxyexecutor.Options{}); errSelect != nil {
		t.Fatalf("valid alias did not become eligible after config reload: %v", errSelect)
	}
	if got := manager.ResolveExecutionModel(credential, aliasModel); got != hiddenModel {
		t.Fatalf("valid alias upstream model = %q, want %q", got, hiddenModel)
	}

	service.applyWatcherConfigUpdate(configForTemplate("missing-template"))
	if !service.refreshModelRegistrationForAuth(credential) {
		t.Fatal("invalid config reload did not refresh credential registration")
	}
	assertInvalid()
}
