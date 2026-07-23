package claude

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestConvertClaudeRequestToCodex_SystemMessageScenarios(t *testing.T) {
	tests := []struct {
		name             string
		inputJSON        string
		wantHasDeveloper bool
		wantTexts        []string
	}{
		{
			name: "No system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: false,
		},
		{
			name: "Empty string system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": "",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: false,
		},
		{
			name: "String system field",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": "Be helpful",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: true,
			wantTexts:        []string{"Be helpful"},
		},
		{
			name: "Message system role does not become developer",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [
					{"role": "system", "content": "Follow the project instructions"},
					{"role": "user", "content": "hello"}
				]
			}`,
			wantHasDeveloper: false,
		},
		{
			name: "Array system field with filtered billing header",
			inputJSON: `{
				"model": "claude-3-opus",
				"system": [
					{"type": "text", "text": "x-anthropic-billing-header: tenant-123"},
					{"type": "text", "text": "Block 1"},
					{"type": "text", "text": "Block 2"}
				],
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantHasDeveloper: true,
			wantTexts:        []string{"Block 1", "Block 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertClaudeRequestToCodex("test-model", []byte(tt.inputJSON), false)
			resultJSON := gjson.ParseBytes(result)
			inputs := resultJSON.Get("input").Array()

			hasDeveloper := len(inputs) > 0 && inputs[0].Get("role").String() == "developer"
			if hasDeveloper != tt.wantHasDeveloper {
				t.Fatalf("got hasDeveloper = %v, want %v. Output: %s", hasDeveloper, tt.wantHasDeveloper, resultJSON.Get("input").Raw)
			}

			if !tt.wantHasDeveloper {
				return
			}

			content := inputs[0].Get("content").Array()
			if len(content) != len(tt.wantTexts) {
				t.Fatalf("got %d system content items, want %d. Content: %s", len(content), len(tt.wantTexts), inputs[0].Get("content").Raw)
			}

			for i, wantText := range tt.wantTexts {
				if gotType := content[i].Get("type").String(); gotType != "input_text" {
					t.Fatalf("content[%d] type = %q, want %q", i, gotType, "input_text")
				}
				if gotText := content[i].Get("text").String(); gotText != wantText {
					t.Fatalf("content[%d] text = %q, want %q", i, gotText, wantText)
				}
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_MessageSystemRoleWrapsAsUserReminder(t *testing.T) {
	inputJSON := `{
		"model": "claude-3-opus",
		"system": [{"type": "text", "text": "Top-level rules"}],
		"messages": [
			{"role": "user", "content": "hello"},
			{"role": "system", "content": "Follow the project instructions"},
			{"role": "assistant", "content": [{"type": "text", "text": "ok"}]},
			{"role": "system", "content": [{"type": "text", "text": "Use the current repo"}]}
		]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	inputs := gjson.GetBytes(result, "input").Array()
	if len(inputs) != 5 {
		t.Fatalf("got %d input items, want 5: %s", len(inputs), gjson.GetBytes(result, "input").Raw)
	}

	if got := inputs[0].Get("role").String(); got != "developer" {
		t.Fatalf("top-level system role = %q, want developer", got)
	}
	if got := inputs[2].Get("role").String(); got != "user" {
		t.Fatalf("message-level system role = %q, want user", got)
	}
	if got := inputs[2].Get("content.0.text").String(); got != "<system-reminder>\nFollow the project instructions\n</system-reminder>" {
		t.Fatalf("unexpected first reminder text: %q", got)
	}
	if got := inputs[4].Get("role").String(); got != "user" {
		t.Fatalf("array message-level system role = %q, want user", got)
	}
	if got := inputs[4].Get("content.0.text").String(); got != "<system-reminder>\nUse the current repo\n</system-reminder>" {
		t.Fatalf("unexpected second reminder text: %q", got)
	}
}

func TestConvertClaudeRequestToCodex_ParallelToolCalls(t *testing.T) {
	tests := []struct {
		name                  string
		inputJSON             string
		wantParallelToolCalls bool
	}{
		{
			name: "Default to true when tool_choice.disable_parallel_tool_use is absent",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: true,
		},
		{
			name: "Disable parallel tool calls when client opts out",
			inputJSON: `{
				"model": "claude-3-opus",
				"tool_choice": {"disable_parallel_tool_use": true},
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: false,
		},
		{
			name: "Keep parallel tool calls enabled when client explicitly allows them",
			inputJSON: `{
				"model": "claude-3-opus",
				"tool_choice": {"disable_parallel_tool_use": false},
				"messages": [{"role": "user", "content": "hello"}]
			}`,
			wantParallelToolCalls: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertClaudeRequestToCodex("test-model", []byte(tt.inputJSON), false)
			resultJSON := gjson.ParseBytes(result)

			if got := resultJSON.Get("parallel_tool_calls").Bool(); got != tt.wantParallelToolCalls {
				t.Fatalf("parallel_tool_calls = %v, want %v. Output: %s", got, tt.wantParallelToolCalls, string(result))
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ServiceTier(t *testing.T) {
	tests := []struct {
		name            string
		serviceTierJSON string
		speedJSON       string
		want            string
		wantExists      bool
	}{
		{
			name:            "Priority passes through",
			serviceTierJSON: `"priority"`,
			want:            "priority",
			wantExists:      true,
		},
		{
			name:            "Fast tier normalizes to priority",
			serviceTierJSON: `"fast"`,
			want:            "priority",
			wantExists:      true,
		},
		{
			name:            "Unsupported tier is omitted",
			serviceTierJSON: `"default"`,
		},
		{
			name:            "Non-string tier is omitted",
			serviceTierJSON: `true`,
		},
		{
			name:       "Fast speed maps to priority",
			speedJSON:  `"fast"`,
			want:       "priority",
			wantExists: true,
		},
		{
			name:      "Standard speed is omitted",
			speedJSON: `"standard"`,
		},
		{
			name:      "Non-string speed is omitted",
			speedJSON: `true`,
		},
		{
			name:            "Fast speed overrides unsupported Anthropic tier",
			serviceTierJSON: `"auto"`,
			speedJSON:       `"fast"`,
			want:            "priority",
			wantExists:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON := []byte(`{
				"model": "gpt-5.4",
				"messages": [{"role": "user", "content": "Reply with OK"}]
			}`)
			if tt.serviceTierJSON != "" {
				inputJSON, _ = sjson.SetRawBytes(inputJSON, "service_tier", []byte(tt.serviceTierJSON))
			}
			if tt.speedJSON != "" {
				inputJSON, _ = sjson.SetRawBytes(inputJSON, "speed", []byte(tt.speedJSON))
			}

			result := ConvertClaudeRequestToCodex("gpt-5.4", inputJSON, false)
			serviceTierResult := gjson.GetBytes(result, "service_tier")
			if serviceTierResult.Exists() != tt.wantExists {
				t.Fatalf("service_tier exists = %v, want %v. Output: %s", serviceTierResult.Exists(), tt.wantExists, string(result))
			}
			if !tt.wantExists {
				return
			}
			if got := serviceTierResult.String(); got != tt.want {
				t.Fatalf("service_tier = %q, want %q. Output: %s", got, tt.want, string(result))
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ShortenLongToolUseIDs(t *testing.T) {
	longID := "toolu_" + strings.Repeat("a", 62)
	if len(longID) <= 64 {
		t.Fatalf("test setup error: longID length = %d, want > 64", len(longID))
	}

	inputJSON := `{
		"model": "claude-3-opus",
		"messages": [
			{"role": "user", "content": [{"type":"text","text":"run pwd"}]},
			{"role": "assistant", "content": [
				{"type":"tool_use","id":"` + longID + `","name":"Bash","input":{"cmd":"pwd"}}
			]},
			{"role": "user", "content": [
				{"type":"tool_result","tool_use_id":"` + longID + `","content":"ok"}
			]}
		]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	inputs := gjson.GetBytes(result, "input").Array()

	var callID string
	var outputCallID string
	for _, item := range inputs {
		switch item.Get("type").String() {
		case "function_call":
			callID = item.Get("call_id").String()
		case "function_call_output":
			outputCallID = item.Get("call_id").String()
		}
	}

	if callID == "" {
		t.Fatalf("missing function_call item. Output: %s", string(result))
	}
	if outputCallID == "" {
		t.Fatalf("missing function_call_output item. Output: %s", string(result))
	}
	if callID != outputCallID {
		t.Fatalf("call_id mismatch: function_call=%q function_call_output=%q. Output: %s", callID, outputCallID, string(result))
	}
	if len(callID) > 64 {
		t.Fatalf("call_id length = %d, want <= 64: %q", len(callID), callID)
	}
	if callID == longID {
		t.Fatalf("long call_id was not shortened: %q", callID)
	}
}

func TestConvertClaudeRequestToCodex_ToolChoiceModeMapping(t *testing.T) {
	tests := []struct {
		name                string
		claudeToolChoice    string
		wantCodexToolChoice string
	}{
		{
			name:                "Any requires at least one tool",
			claudeToolChoice:    `{"type":"any"}`,
			wantCodexToolChoice: "required",
		},
		{
			name:                "None disables tools",
			claudeToolChoice:    `{"type":"none"}`,
			wantCodexToolChoice: "none",
		},
		{
			name:                "Auto stays auto",
			claudeToolChoice:    `{"type":"auto"}`,
			wantCodexToolChoice: "auto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON := `{
				"model": "claude-3-opus",
				"tools": [
					{"name": "lookup", "description": "Lookup", "input_schema": {"type":"object","properties":{}}}
				],
				"tool_choice": ` + tt.claudeToolChoice + `,
				"messages": [{"role": "user", "content": "hello"}]
			}`

			result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
			resultJSON := gjson.ParseBytes(result)

			if got := resultJSON.Get("tool_choice").String(); got != tt.wantCodexToolChoice {
				t.Fatalf("tool_choice = %q, want %q. Output: %s", got, tt.wantCodexToolChoice, string(result))
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ToolChoiceSpecificFunctionUsesConvertedName(t *testing.T) {
	longName := "mcp__server_with_a_very_long_name_that_exceeds_sixty_four_characters__search"
	inputJSON := `{
		"model": "claude-3-opus",
		"tools": [
			{"name": "` + longName + `", "description": "Search", "input_schema": {"type":"object","properties":{}}}
		],
		"tool_choice": {"type":"tool","name":"` + longName + `"},
		"messages": [{"role": "user", "content": "hello"}]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	resultJSON := gjson.ParseBytes(result)

	if got := resultJSON.Get("tool_choice.type").String(); got != "function" {
		t.Fatalf("tool_choice.type = %q, want function. Output: %s", got, string(result))
	}
	toolName := resultJSON.Get("tools.0.name").String()
	choiceName := resultJSON.Get("tool_choice.name").String()
	if choiceName != toolName {
		t.Fatalf("tool_choice.name = %q, want converted tool name %q. Output: %s", choiceName, toolName, string(result))
	}
	if choiceName == longName {
		t.Fatalf("tool_choice.name should use shortened Codex tool name. Output: %s", string(result))
	}
}

func TestConvertClaudeRequestToCodex_WebSearchToolMapping(t *testing.T) {
	inputJSON := `{
		"model": "claude-3-opus",
		"tools": [
			{
				"type": "web_search_20260209",
				"name": "web_search",
				"allowed_domains": ["example.com"],
				"blocked_domains": ["blocked.example"],
				"user_location": {
					"type": "approximate",
					"city": "Beijing",
					"country": "CN",
					"timezone": "Asia/Shanghai"
				}
			}
		],
		"tool_choice": {"type":"tool","name":"web_search"},
		"messages": [{"role": "user", "content": "hello"}]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	resultJSON := gjson.ParseBytes(result)

	if got := resultJSON.Get("tools.0.type").String(); got != "web_search" {
		t.Fatalf("tools.0.type = %q, want web_search. Output: %s", got, string(result))
	}
	if got := resultJSON.Get("tools.0.filters.allowed_domains.0").String(); got != "example.com" {
		t.Fatalf("tools.0.filters.allowed_domains.0 = %q, want example.com. Output: %s", got, string(result))
	}
	if resultJSON.Get("tools.0.blocked_domains").Exists() {
		t.Fatalf("tools.0.blocked_domains should not be forwarded to Codex. Output: %s", string(result))
	}
	if got := resultJSON.Get("tools.0.user_location.city").String(); got != "Beijing" {
		t.Fatalf("tools.0.user_location.city = %q, want Beijing. Output: %s", got, string(result))
	}
	if got := resultJSON.Get("tool_choice.type").String(); got != "web_search" {
		t.Fatalf("tool_choice.type = %q, want web_search. Output: %s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_WebSearchToolChoiceUsesDeclaredTypedToolName(t *testing.T) {
	inputJSON := `{
		"model": "claude-opus-4-7",
		"tools": [
			{"type": "web_search_20250305", "name": "browser_search"},
			{"name": "web_search", "description": "Local search", "input_schema": {"type":"object","properties":{}}}
		],
		"tool_choice": {"type":"tool","name":"web_search"},
		"messages": [{"role": "user", "content": "hello"}]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	resultJSON := gjson.ParseBytes(result)

	if got := resultJSON.Get("tool_choice.type").String(); got != "function" {
		t.Fatalf("tool_choice.type = %q, want function. Output: %s", got, string(result))
	}
	if got := resultJSON.Get("tool_choice.name").String(); got != "web_search" {
		t.Fatalf("tool_choice.name = %q, want web_search. Output: %s", got, string(result))
	}
}

func TestConvertClaudeRequestToCodex_AssistantThinkingSignatureToReasoningItem(t *testing.T) {
	signature := validCodexReasoningSignature()
	inputJSON := `{
		"model": "claude-3-opus",
		"messages": [
			{
				"role": "assistant",
				"content": [
					{
						"type": "thinking",
						"thinking": "visible summary must not be replayed",
						"signature": "` + signature + `"
					},
					{
						"type": "text",
						"text": "visible answer"
					}
				]
			},
			{
				"role": "user",
				"content": "continue"
			}
		]
	}`

	result := ConvertClaudeRequestToCodex("test-model", []byte(inputJSON), false)
	resultJSON := gjson.ParseBytes(result)
	inputs := resultJSON.Get("input").Array()
	if len(inputs) != 3 {
		t.Fatalf("got %d input items, want 3. Output: %s", len(inputs), string(result))
	}

	reasoning := inputs[0]
	if got := reasoning.Get("type").String(); got != "reasoning" {
		t.Fatalf("first input type = %q, want reasoning. Output: %s", got, string(result))
	}
	if got := reasoning.Get("encrypted_content").String(); got != signature {
		t.Fatalf("encrypted_content = %q, want %q", got, signature)
	}
	if got := reasoning.Get("summary").Raw; got != "[]" {
		t.Fatalf("summary = %s, want []", got)
	}
	if got := reasoning.Get("content").Raw; got != "null" {
		t.Fatalf("content = %s, want null", got)
	}

	assistantMessage := inputs[1]
	if got := assistantMessage.Get("role").String(); got != "assistant" {
		t.Fatalf("second input role = %q, want assistant. Output: %s", got, string(result))
	}
	if got := assistantMessage.Get("content.0.type").String(); got != "output_text" {
		t.Fatalf("assistant content type = %q, want output_text", got)
	}
	if got := assistantMessage.Get("content.0.text").String(); got != "visible answer" {
		t.Fatalf("assistant text = %q, want visible answer", got)
	}
	if strings.Contains(string(result), "visible summary must not be replayed") {
		t.Fatalf("thinking text should not be replayed into Codex input. Output: %s", string(result))
	}
}

func TestConvertClaudeRequestToCodex_PreservesContentOrderAcrossToolAndReasoningItems(t *testing.T) {
	signature := validCodexReasoningSignature()
	inputJSON := `{
		"system": "system rules",
		"messages": [
			{"role":"assistant","content":[
				{"type":"text","text":"before reasoning"},
				{"type":"thinking","signature":"` + signature + `"},
				{"type":"text","text":"before tool"},
				{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"query":"test"}},
				{"type":"text","text":"after tool"}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":[
					{"type":"text","text":"tool output"},
					{"type":"image","source":{"media_type":"image/png","data":"aW1hZ2U="}}
				]},
				{"type":"text","text":"continue"}
			]}
		],
		"tools": [{"name":"lookup","input_schema":{"type":"object"}}]
	}`

	result := ConvertClaudeRequestToCodex("gpt-5.4", []byte(inputJSON), false)
	inputs := gjson.GetBytes(result, "input").Array()
	if len(inputs) != 8 {
		t.Fatalf("got %d input items, want 8. Output: %s", len(inputs), result)
	}

	wantTypes := []string{"message", "message", "reasoning", "message", "function_call", "message", "function_call_output", "message"}
	for i := 0; i < len(wantTypes); i++ {
		if got := inputs[i].Get("type").String(); got != wantTypes[i] {
			t.Fatalf("input[%d].type = %q, want %q. Output: %s", i, got, wantTypes[i], result)
		}
	}

	if got := inputs[1].Get("content.0.text").String(); got != "before reasoning" {
		t.Fatalf("input[1] text = %q, want before reasoning", got)
	}
	if got := inputs[3].Get("content.0.text").String(); got != "before tool" {
		t.Fatalf("input[3] text = %q, want before tool", got)
	}
	if got := inputs[5].Get("content.0.text").String(); got != "after tool" {
		t.Fatalf("input[5] text = %q, want after tool", got)
	}
	if got := inputs[6].Get("output.0.type").String(); got != "input_text" {
		t.Fatalf("tool result output.0.type = %q, want input_text", got)
	}
	if got := inputs[6].Get("output.1.image_url").String(); got != "data:image/png;base64,aW1hZ2U=" {
		t.Fatalf("tool result image_url = %q, want data URL", got)
	}
	if got := inputs[7].Get("content.0.text").String(); got != "continue" {
		t.Fatalf("input[7] text = %q, want continue", got)
	}
}

func TestConvertClaudeRequestToCodex_AssistantGrokSignatureToReasoningItem(t *testing.T) {
	signature := "HmlYdr2aCAqCYP/m9mr8PS6KOsdMs72FGDigmydR+Jsmuv8KX97yWPlbOwmXJgWn0CbHaCacdQD3+n5EvpgLfPNmafS3kdICBjRuDf4bzHy7uBiUhNVhqPtp/ee1y9q4imPE4LYgD1VZ4J+bp9mTeqA1+nC9Oue58CiNEMV9SVaGenCD+aBnVuSTzQhD32Y+68i6HLJW0Dx6ifaRfb8hxYtA/sPM+/FTvAMW11nRho5a2BBSkpnzfqqAz/e/vGJ77/bygpXM823QA9wL9i0X"
	payload := []byte(`{"model":"grok-4.5","messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"summary","signature":""},{"type":"text","text":"answer"}]},{"role":"user","content":"next"}]}`)
	payload, _ = sjson.SetBytes(payload, "messages.0.content.0.signature", signature)

	out := ConvertClaudeRequestToCodex("grok-4.5", payload, false)
	reasoning := gjson.GetBytes(out, "input.0")
	if reasoning.Get("type").String() != "reasoning" {
		t.Fatalf("input.0 type = %q, want reasoning; output=%s", reasoning.Get("type").String(), out)
	}
	if got := reasoning.Get("encrypted_content").String(); got != signature {
		t.Fatalf("encrypted_content = %q, want Grok signature", got)
	}
}

func TestConvertClaudeRequestToCodex_IgnoresGrokSignatureForNonGrokTargets(t *testing.T) {
	signature := "HmlYdr2aCAqCYP/m9mr8PS6KOsdMs72FGDigmydR+Jsmuv8KX97yWPlbOwmXJgWn0CbHaCacdQD3+n5EvpgLfPNmafS3kdICBjRuDf4bzHy7uBiUhNVhqPtp/ee1y9q4imPE4LYgD1VZ4J+bp9mTeqA1+nC9Oue58CiNEMV9SVaGenCD+aBnVuSTzQhD32Y+68i6HLJW0Dx6ifaRfb8hxYtA/sPM+/FTvAMW11nRho5a2BBSkpnzfqqAz/e/vGJ77/bygpXM823QA9wL9i0X"
	payload := []byte(`{"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"summary","signature":""},{"type":"text","text":"answer"}]},{"role":"user","content":"next"}]}`)
	payload, _ = sjson.SetBytes(payload, "messages.0.content.0.signature", signature)

	for _, modelName := range []string{"gpt-5.4", "claude-sonnet-4-6"} {
		t.Run(modelName, func(t *testing.T) {
			out := ConvertClaudeRequestToCodex(modelName, payload, false)
			if got := countRequestInputItemsByType(out, "reasoning"); got != 0 {
				t.Fatalf("got %d reasoning items for non-Grok target, want 0; output=%s", got, out)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_IgnoresNonCodexThinkingSignatures(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
	}{
		{
			name: "Ignore user thinking even with Codex-shaped signature",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [
					{
						"role": "user",
						"content": [
							{
								"type": "thinking",
								"thinking": "user supplied thinking",
								"signature": "` + validCodexReasoningSignature() + `"
							},
							{
								"type": "text",
								"text": "hello"
							}
						]
					}
				]
			}`,
		},
		{
			name: "Ignore Anthropic native signature",
			inputJSON: `{
				"model": "claude-3-opus",
				"messages": [
					{
						"role": "assistant",
						"content": [
							{
								"type": "thinking",
								"thinking": "anthropic thinking",
								"signature": "Eo8Canthropic-state"
							},
							{
								"type": "text",
								"text": "visible answer"
							}
						]
					}
				]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertClaudeRequestToCodex("test-model", []byte(tt.inputJSON), false)
			if got := countRequestInputItemsByType(result, "reasoning"); got != 0 {
				t.Fatalf("got %d reasoning items, want 0. Output: %s", got, string(result))
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_PreservesStrictAndStructuredOutput(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"max_tokens":123,
		"output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"answer":{"type":"string"},"note":{"type":"string"}},"required":["answer","note"],"additionalProperties":false}}},
		"tools":[{"name":"answer_tool","strict":true,"input_schema":{"type":"object","properties":{"required_value":{"type":"string"},"optional_value":{"type":"string"}},"required":["required_value","optional_value"],"additionalProperties":false}}],
		"messages":[{"role":"user","content":"hello"}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if !result.Get("tools.0.strict").Bool() {
		t.Fatalf("strict tool setting lost: %s", result.Raw)
	}
	if result.Get("max_output_tokens").Int() != 123 {
		t.Fatalf("max_output_tokens = %s", result.Get("max_output_tokens").Raw)
	}
	if result.Get("text.format.type").String() != "json_schema" || !result.Get("text.format.strict").Bool() {
		t.Fatalf("structured output missing: %s", result.Raw)
	}
	if !result.Get("text.format.schema.properties.answer").Exists() {
		t.Fatalf("structured output schema missing: %s", result.Raw)
	}
	if result.Get("text.format.schema.required.#").Int() != 2 || result.Get("text.format.schema.additionalProperties").Type != gjson.False {
		t.Fatalf("strict output schema not normalized: %s", result.Raw)
	}
	if result.Get("tools.0.parameters.required.#").Int() != 2 || result.Get("tools.0.parameters.additionalProperties").Type != gjson.False {
		t.Fatalf("strict tool schema not normalized: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_OrdinaryToolsAreNonStrict(t *testing.T) {
	for _, toolType := range []string{"", `"type":"custom",`} {
		input := []byte(`{"model":"claude-opus-4-8","tools":[{` + toolType + `"name":"lookup","input_schema":{"type":"object","properties":{"value":{"type":"string"}}}}],"messages":[{"role":"user","content":"hello"}]}`)
		result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
		if result.Get("tools.0.type").String() != "function" {
			t.Fatalf("ordinary tool type = %s, want function: %s", result.Get("tools.0.type").Raw, result.Raw)
		}
		if result.Get("tools.0.strict").Type != gjson.False {
			t.Fatalf("ordinary tool strict = %s, want false: %s", result.Get("tools.0.strict").Raw, result.Raw)
		}
	}
}

func TestConvertClaudeRequestToCodex_ThinkingDefaultsFollowClaudeModel(t *testing.T) {
	tests := []struct {
		model         string
		wantEffort    string
		wantEncrypted bool
	}{
		{model: "claude-fable-5", wantEffort: "high", wantEncrypted: true},
		{model: "claude-fable-5[1m]", wantEffort: "high", wantEncrypted: true},
		{model: "claude-fable-5-internal", wantEffort: "medium", wantEncrypted: true},
		{model: "claude-mythos-preview", wantEffort: "high", wantEncrypted: true},
		{model: "claude-sonnet-5", wantEffort: "high", wantEncrypted: true},
		{model: "claude-opus-4-8", wantEffort: "none"},
		{model: "claude-sonnet-4-6", wantEffort: "none"},
		{model: "user-defined-model", wantEffort: "medium", wantEncrypted: true},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			input := []byte(`{"model":"` + test.model + `","messages":[{"role":"user","content":"hello"}]}`)
			result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
			if got := result.Get("reasoning.effort").String(); got != test.wantEffort {
				t.Fatalf("reasoning effort = %q, want %q: %s", got, test.wantEffort, result.Raw)
			}
			if got := result.Get("include.0").Exists(); got != test.wantEncrypted {
				t.Fatalf("encrypted content include = %v, want %v: %s", got, test.wantEncrypted, result.Raw)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_Adaptive46DefaultsToSummarizedThinking(t *testing.T) {
	for _, model := range []string{"claude-opus-4-6", "claude-sonnet-4-6"} {
		t.Run(model, func(t *testing.T) {
			input := []byte(`{"model":"` + model + `","thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hello"}]}`)
			result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
			if result.Get("reasoning.summary").String() != "auto" {
				t.Fatalf("reasoning summary missing: %s", result.Raw)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ThinkingDisplayOverridesDefaults(t *testing.T) {
	tests := []struct {
		name        string
		thinking    string
		wantSummary bool
	}{
		{name: "manual default summarized", thinking: `{"type":"enabled","budget_tokens":2048}`, wantSummary: true},
		{name: "manual explicitly omitted", thinking: `{"type":"enabled","budget_tokens":2048,"display":"omitted"}`},
		{name: "adaptive explicitly summarized", thinking: `{"type":"adaptive","display":"summarized"}`, wantSummary: true},
		{name: "adaptive explicitly omitted", thinking: `{"type":"adaptive","display":"omitted"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(`{"model":"claude-opus-4-6","thinking":` + test.thinking + `,"messages":[{"role":"user","content":"hello"}]}`)
			result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
			if got := result.Get("reasoning.summary").String() == "auto"; got != test.wantSummary {
				t.Fatalf("reasoning summary = %v, want %v: %s", got, test.wantSummary, result.Raw)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_PreservesToolResultError(t *testing.T) {
	input := []byte(`{
		"model":"claude-fable-5",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"check","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","is_error":true,"content":"opaque failure"}]}
		]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	output := result.Get("input.#(type==\"function_call_output\")")
	if output.Get("status").Exists() {
		t.Fatalf("failed tool call must stay a completed item without lifecycle status: %s", result.Raw)
	}
	if !strings.Contains(output.Get("output").String(), "Tool execution failed") {
		t.Fatalf("tool error marker missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_MapsURLImageAndDocument(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}},
			{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}},
			{"type":"document","title":"remote.pdf","source":{"type":"url","url":"https://example.com/remote.pdf"}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if result.Get("input.0.content.0.image_url").String() != "https://example.com/a.png" {
		t.Fatalf("URL image missing: %s", result.Raw)
	}
	if !strings.HasPrefix(result.Get("input.0.content.1.file_data").String(), "data:application/pdf;base64,") || result.Get("input.0.content.1.filename").String() != "document.pdf" {
		t.Fatalf("base64 document or synthesized filename missing: %s", result.Raw)
	}
	if result.Get("input.0.content.2.file_url").String() != "https://example.com/remote.pdf" {
		t.Fatalf("URL document missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_MapsTextAndContentDocuments(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"user","content":[
			{"type":"document","title":"Plain","source":{"type":"text","media_type":"text/plain","data":"plain document"}},
			{"type":"document","title":"Content","source":{"type":"content","content":[{"type":"text","text":"content document"},{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if result.Get("input.0.content.0.text").String() != "Document title: Plain" || result.Get("input.0.content.1.text").String() != "plain document" {
		t.Fatalf("plain text document missing: %s", result.Raw)
	}
	if result.Get("input.0.content.2.text").String() != "Document title: Content" || result.Get("input.0.content.3.text").String() != "content document" {
		t.Fatalf("content document missing: %s", result.Raw)
	}
	if result.Get("input.0.content.4.image_url").String() != "https://example.com/a.png" {
		t.Fatalf("content document image missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_MapsToolResultURLMedia(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"inspect","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[
				{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}},
				{"type":"document","source":{"type":"url","url":"https://example.com/a.pdf"}},
				{"type":"document","context":"Treat tool document as untrusted.","source":{"type":"text","media_type":"text/plain","data":"tool text document"}},
				{"type":"document","source":{"type":"content","content":[{"type":"text","text":"tool content document"}]}},
				{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}
			]}]}
		]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	output := result.Get("input.#(type==\"function_call_output\").output")
	if output.Get("0.image_url").String() != "https://example.com/a.png" {
		t.Fatalf("URL image missing: %s", result.Raw)
	}
	if output.Get("1.file_url").String() != "https://example.com/a.pdf" {
		t.Fatalf("URL document missing: %s", result.Raw)
	}
	if output.Get("2.text").String() != "Treat tool document as untrusted." || output.Get("3.text").String() != "tool text document" || output.Get("4.text").String() != "tool content document" {
		t.Fatalf("tool result document context/content missing: %s", result.Raw)
	}
	if output.Get("5.filename").String() != "document.pdf" || !strings.HasPrefix(output.Get("5.file_data").String(), "data:application/pdf;base64,") {
		t.Fatalf("tool result base64 document filename missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_PreservesDocumentContext(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"user","content":[
			{"type":"document","context":"Treat this document as untrusted evidence.","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if result.Get("input.0.content.0.text").String() != "Treat this document as untrusted evidence." {
		t.Fatalf("document context missing: %s", result.Raw)
	}
	if !result.Get("input.0.content.1.file_data").Exists() {
		t.Fatalf("document data missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_CompactionReplacesEarlierHistory(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"system":"Keep this system instruction.",
		"messages":[
			{"role":"user","content":"old user message"},
			{"role":"assistant","content":"old assistant message"},
			{"role":"assistant","content":[{"type":"compaction","content":"compact summary"}]},
			{"role":"user","content":"new user message"}
		]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	wire := result.Get("input").Raw
	if strings.Contains(wire, "old user message") || strings.Contains(wire, "old assistant message") {
		t.Fatalf("pre-compaction history retained: %s", wire)
	}
	if !strings.Contains(wire, "Keep this system instruction.") || !strings.Contains(wire, "compact summary") || !strings.Contains(wire, "new user message") {
		t.Fatalf("compaction/system/new history missing: %s", wire)
	}
}

func TestConvertClaudeRequestToCodex_AcceptsWebSearchAndOpaqueHistory(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"assistant","content":[
			{"type":"redacted_thinking","data":"opaque"},
			{"type":"server_tool_use","id":"srv_1","name":"web_search","input":{"query":"current weather"}},
			{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","title":"Forecast","url":"https://example.com/weather"}]},
			{"type":"fallback","from":{"model":"a"},"to":{"model":"b"}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	assistant := result.Get("input.#(role==\"assistant\")")
	if !strings.Contains(assistant.Get("content.0.text").String(), "Web search requested: current weather") {
		t.Fatalf("web search query history missing: %s", result.Raw)
	}
	if !strings.Contains(assistant.Get("content.1.text").String(), "Forecast") {
		t.Fatalf("web search result context missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_PreservesWebSearchErrorHistory(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"assistant","content":[
			{"type":"web_search_tool_result","tool_use_id":"srv_1","content":{"type":"web_search_tool_result_error","error_code":"unavailable"}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if !strings.Contains(result.Get("input.0.content.0.text").String(), "Web search failed: unavailable") {
		t.Fatalf("web search error missing: %s", result.Raw)
	}
}

func TestConvertClaudeRequestToCodex_PreservesMediaAliases(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[{"role":"user","content":[
			{"type":"image","source":{"type":"base64","mime_type":"image/png","base64":"AA=="}},
			{"type":"document","source":{"type":"content","content":[{"type":"image","source":{"type":"base64","mime_type":"image/webp","base64":"AQ=="}}]}}
		]}]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	if got := result.Get("input.0.content.0.image_url").String(); got != "data:image/png;base64,AA==" {
		t.Fatalf("top-level image alias changed: %q", got)
	}
	if got := result.Get("input.0.content.1.image_url").String(); got != "data:image/webp;base64,AQ==" {
		t.Fatalf("nested document image alias changed: %q", got)
	}
}

func TestConvertClaudeRequestToCodex_MapsSearchResultsAsAttributedText(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"messages":[
			{"role":"user","content":[{"type":"search_result","source":"https://example.com/a","title":"Direct result","content":[{"type":"text","text":"direct evidence"}]}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"search","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"search_result","source":"kb://item-1","title":"Tool result","content":[{"type":"text","text":"tool evidence"}]}]}]}
		]
	}`)
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
	direct := result.Get("input.0.content.0.text").String()
	if !strings.Contains(direct, "Direct result") || !strings.Contains(direct, "https://example.com/a") || !strings.Contains(direct, "direct evidence") {
		t.Fatalf("direct search result attribution missing: %s", result.Raw)
	}
	toolOutput := result.Get("input.#(type==\"function_call_output\").output.0.text").String()
	if !strings.Contains(toolOutput, "Tool result") || !strings.Contains(toolOutput, "kb://item-1") || !strings.Contains(toolOutput, "tool evidence") {
		t.Fatalf("tool search result attribution missing: %s", result.Raw)
	}
}

func countRequestInputItemsByType(result []byte, itemType string) int {
	count := 0
	gjson.GetBytes(result, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == itemType {
			count++
		}
		return true
	})
	return count
}

func validCodexReasoningSignature() string {
	raw := make([]byte, 1+8+16+16+32)
	raw[0] = 0x80
	raw[8] = 1
	return base64.URLEncoding.EncodeToString(raw)
}

func TestCodexInlineDocumentFilenameEnsuresMediaTypeExtension(t *testing.T) {
	pdf := "data:application/pdf;base64,AAAA"
	if got := codexInlineDocumentFilename(pdf, "Quarterly report"); got != "Quarterly report.pdf" {
		t.Fatalf("titled pdf = %q, want extension appended", got)
	}
	if got := codexInlineDocumentFilename(pdf, "report.pdf"); got != "report.pdf" {
		t.Fatalf("already-suffixed = %q, want unchanged", got)
	}
	if got := codexInlineDocumentFilename(pdf, "  "); got != "document.pdf" {
		t.Fatalf("empty title = %q, want synthetic name", got)
	}
	if got := codexInlineDocumentFilename("data:text/plain;base64,AAAA", "Notes"); got != "Notes.txt" {
		t.Fatalf("text title = %q, want .txt appended", got)
	}
}

func TestCodexInlineFileExtensionIsCaseInsensitive(t *testing.T) {
	if got := codexInlineDocumentFilename("data:Application/PDF;base64,AAAA", "Report"); got != "Report.pdf" {
		t.Fatalf("mixed-case media type = %q, want .pdf", got)
	}
}

func TestConvertClaudeRequestToCodex_ManualThinkingDisplayDefaultsOmittedOnNewestModels(t *testing.T) {
	build := func(model string) []byte {
		return []byte(`{"model":"` + model + `","thinking":{"type":"enabled","budget_tokens":8000},"messages":[{"role":"user","content":"hi"}]}`)
	}
	for _, model := range []string{"claude-fable-5", "claude-mythos-preview", "claude-opus-4-8"} {
		result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", build(model), false))
		if got := result.Get("reasoning.summary").String(); got == "auto" {
			t.Fatalf("%s: implicit display must stay omitted, got summary=%q", model, got)
		}
	}
	// Older models keep the summarized manual-thinking default.
	result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", build("claude-opus-4-6"), false))
	if got := result.Get("reasoning.summary").String(); got != "auto" {
		t.Fatalf("claude-opus-4-6: want summarized default, got %q", got)
	}
	// Explicit summarized still wins on newest models.
	explicit := []byte(`{"model":"claude-fable-5","thinking":{"type":"enabled","display":"summarized"},"messages":[{"role":"user","content":"hi"}]}`)
	result = gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", explicit, false))
	if got := result.Get("reasoning.summary").String(); got != "auto" {
		t.Fatalf("explicit summarized ignored, got %q", got)
	}
}

func TestConvertClaudeRequestToCodex_ExplicitEffortSurvivesWithoutThinking(t *testing.T) {
	for _, model := range []string{"claude-opus-4-8", "claude-custom-alias"} {
		input := []byte(`{"model":"` + model + `","output_config":{"effort":"max"},"messages":[{"role":"user","content":"hi"}]}`)
		result := gjson.ParseBytes(ConvertClaudeRequestToCodex("gpt-5.6-sol", input, false))
		if got := result.Get("reasoning.effort").String(); got != "max" {
			t.Fatalf("%s: explicit effort dropped, reasoning.effort = %q", model, got)
		}
	}
}
