package config

import (
	"encoding/json"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSanitizeOAuthModelAlias_PreservesOptionalFields(t *testing.T) {
	cfg := &Config{
		OAuthModelAlias: map[string][]OAuthModelAlias{
			" CoDeX ": {
				{Name: " gpt-5 ", Alias: " g5 ", Template: " gpt-5.6-sol ", Fork: true, DisplayName: " GPT Five ", ForceMapping: true},
				{Name: "gpt-6", Alias: "g6"},
			},
		},
	}

	cfg.SanitizeOAuthModelAlias()

	aliases := cfg.OAuthModelAlias["codex"]
	if len(aliases) != 2 {
		t.Fatalf("expected 2 sanitized aliases, got %d", len(aliases))
	}
	if aliases[0].Name != "gpt-5" || aliases[0].Alias != "g5" || aliases[0].Template != "gpt-5.6-sol" || !aliases[0].Fork || aliases[0].DisplayName != "GPT Five" || !aliases[0].ForceMapping {
		t.Fatalf("unexpected sanitized first alias: %+v", aliases[0])
	}
	if aliases[1].Name != "gpt-6" || aliases[1].Alias != "g6" || aliases[1].Template != "" || aliases[1].Fork || aliases[1].DisplayName != "" || aliases[1].ForceMapping {
		t.Fatalf("unexpected sanitized second alias: %+v", aliases[1])
	}
}

func TestOAuthModelAlias_TemplateJSONAndYAMLRoundTrip(t *testing.T) {
	want := OAuthModelAlias{
		Name:         "hidden-codex-model",
		Alias:        "codex-latest",
		Template:     "gpt-5.6-sol",
		DisplayName:  "Latest Codex",
		ForceMapping: true,
	}

	t.Run("JSON", func(t *testing.T) {
		data, errMarshal := json.Marshal(want)
		if errMarshal != nil {
			t.Fatalf("marshal alias: %v", errMarshal)
		}
		var got OAuthModelAlias
		if errUnmarshal := json.Unmarshal(data, &got); errUnmarshal != nil {
			t.Fatalf("unmarshal alias: %v", errUnmarshal)
		}
		if got != want {
			t.Fatalf("JSON round trip = %+v, want %+v", got, want)
		}
	})

	t.Run("YAML", func(t *testing.T) {
		data, errMarshal := yaml.Marshal(want)
		if errMarshal != nil {
			t.Fatalf("marshal alias: %v", errMarshal)
		}
		var got OAuthModelAlias
		if errUnmarshal := yaml.Unmarshal(data, &got); errUnmarshal != nil {
			t.Fatalf("unmarshal alias: %v", errUnmarshal)
		}
		if got != want {
			t.Fatalf("YAML round trip = %+v, want %+v", got, want)
		}
	})
}

func TestSanitizeOAuthModelAlias_AllowsMultipleAliasesForSameName(t *testing.T) {
	cfg := &Config{
		OAuthModelAlias: map[string][]OAuthModelAlias{
			"antigravity": {
				{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5-20251101", Fork: true},
				{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5-20251101-thinking", Fork: true},
				{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5", Fork: true},
			},
		},
	}

	cfg.SanitizeOAuthModelAlias()

	aliases := cfg.OAuthModelAlias["antigravity"]
	expected := []OAuthModelAlias{
		{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5-20251101", Fork: true},
		{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5-20251101-thinking", Fork: true},
		{Name: "gemini-claude-opus-4-5-thinking", Alias: "claude-opus-4-5", Fork: true},
	}
	if len(aliases) != len(expected) {
		t.Fatalf("expected %d sanitized aliases, got %d", len(expected), len(aliases))
	}
	for i, exp := range expected {
		if aliases[i].Name != exp.Name || aliases[i].Alias != exp.Alias || aliases[i].Fork != exp.Fork {
			t.Fatalf("expected alias %d to be name=%q alias=%q fork=%v, got name=%q alias=%q fork=%v", i, exp.Name, exp.Alias, exp.Fork, aliases[i].Name, aliases[i].Alias, aliases[i].Fork)
		}
	}
}
