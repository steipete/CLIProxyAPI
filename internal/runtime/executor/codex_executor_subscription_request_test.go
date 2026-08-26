package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestApplyCodexSourceRequestCompatibilityScopesOutputTokenLimit(t *testing.T) {
	tests := []struct {
		name       string
		from       sdktranslator.Format
		auth       *cliproxyauth.Auth
		wantAbsent bool
	}{
		{
			name:       "Claude OAuth removes output limit",
			from:       sdktranslator.FormatClaude,
			auth:       &cliproxyauth.Auth{Attributes: map[string]string{cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindOAuth}},
			wantAbsent: true,
		},
		{
			name: "Claude API key preserves output limit",
			from: sdktranslator.FormatClaude,
			auth: &cliproxyauth.Auth{Attributes: map[string]string{cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindAPIKey}},
		},
		{
			name: "Responses OAuth preserves output limit",
			from: sdktranslator.FormatOpenAIResponse,
			auth: &cliproxyauth.Auth{Attributes: map[string]string{cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindOAuth}},
		},
		{
			name: "OpenAI OAuth preserves output limit",
			from: sdktranslator.FormatOpenAI,
			auth: &cliproxyauth.Auth{Attributes: map[string]string{cliproxyauth.AttributeAuthKind: cliproxyauth.AuthKindOAuth}},
		},
		{
			name: "Claude without credential preserves output limit",
			from: sdktranslator.FormatClaude,
		},
		{
			name: "Claude with unknown credential preserves output limit",
			from: sdktranslator.FormatClaude,
			auth: &cliproxyauth.Auth{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := applyCodexSourceRequestCompatibility([]byte(`{"model":"codex-latest","max_output_tokens":123,"input":[{"role":"user","content":"hello"}]}`), test.from, test.auth)
			maxOutputTokens := gjson.GetBytes(body, "max_output_tokens")
			if test.wantAbsent {
				if maxOutputTokens.Exists() {
					t.Fatalf("unexpected max_output_tokens: %s", maxOutputTokens.Raw)
				}
			} else if maxOutputTokens.Int() != 123 {
				t.Fatalf("max_output_tokens = %s, want 123", maxOutputTokens.Raw)
			}
			if got := gjson.GetBytes(body, "input.0.content").String(); got != "hello" {
				t.Fatalf("input = %q, want hello", got)
			}
		})
	}
}

func TestCodexExecutorsClaudeSubscriptionRequestCompatibility(t *testing.T) {
	for _, model := range []string{"codex-latest", "gpt-5.6-sol"} {
		for _, transport := range []string{"http", "websocket"} {
			for _, authKind := range []string{cliproxyauth.AuthKindOAuth, cliproxyauth.AuthKindAPIKey} {
				for _, stream := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/stream=%t", model, transport, authKind, stream)
					t.Run(name, func(t *testing.T) {
						upstreamRequests := make(chan []byte, 1)
						upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
						completed := []byte(`{"type":"response.completed","response":{"id":"resp_subscription","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
							if transport == "websocket" {
								connection, errUpgrade := upgrader.Upgrade(w, request, nil)
								if errUpgrade != nil {
									t.Errorf("upgrade websocket: %v", errUpgrade)
									return
								}
								defer func() { _ = connection.Close() }()
								_, body, errRead := connection.ReadMessage()
								if errRead != nil {
									t.Errorf("read websocket request: %v", errRead)
									return
								}
								upstreamRequests <- body
								if errWrite := connection.WriteMessage(websocket.TextMessage, completed); errWrite != nil {
									t.Errorf("write websocket response: %v", errWrite)
								}
								return
							}

							body, errRead := io.ReadAll(request.Body)
							if errRead != nil {
								t.Errorf("read HTTP request: %v", errRead)
								return
							}
							upstreamRequests <- body
							w.Header().Set("Content-Type", "text/event-stream")
							if _, errWrite := fmt.Fprintf(w, "data: %s\n\n", completed); errWrite != nil {
								t.Errorf("write HTTP response: %v", errWrite)
							}
						}))
						t.Cleanup(server.Close)

						cfg := &config.Config{
							SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll},
							Payload: config.PayloadConfig{Override: []config.PayloadRule{{
								Models: []config.PayloadModelRule{{Name: "*", Protocol: "codex"}},
								Params: map[string]any{"max_output_tokens": 4096},
							}}},
						}
						auth := &cliproxyauth.Auth{
							ID:       "codex-subscription-route-test",
							Provider: "codex",
							Attributes: map[string]string{
								cliproxyauth.AttributeAuthKind: authKind,
								"base_url":                     server.URL,
							},
						}
						if authKind == cliproxyauth.AuthKindOAuth {
							auth.Metadata = map[string]any{"access_token": "test-subscription-token"}
						} else {
							auth.Attributes[cliproxyauth.AttributeAPIKey] = "test-api-key"
						}
						req := cliproxyexecutor.Request{
							Model:   model,
							Payload: []byte(fmt.Sprintf(`{"model":%q,"max_tokens":123,"system":"Follow the request.","speed":"fast","output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}}},"tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}}],"messages":[{"role":"user","content":"hello"}]}`, model)),
						}
						opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, Stream: stream}

						if transport == "websocket" {
							executor := NewCodexWebsocketsExecutor(cfg)
							if stream {
								result, errExecute := executor.ExecuteStream(context.Background(), auth, req, opts)
								if errExecute != nil {
									t.Fatalf("ExecuteStream() error = %v", errExecute)
								}
								for chunk := range result.Chunks {
									if chunk.Err != nil {
										t.Fatalf("stream chunk error = %v", chunk.Err)
									}
								}
							} else if _, errExecute := executor.Execute(context.Background(), auth, req, opts); errExecute != nil {
								t.Fatalf("Execute() error = %v", errExecute)
							}
						} else {
							executor := NewCodexExecutor(cfg)
							if stream {
								result, errExecute := executor.ExecuteStream(context.Background(), auth, req, opts)
								if errExecute != nil {
									t.Fatalf("ExecuteStream() error = %v", errExecute)
								}
								for chunk := range result.Chunks {
									if chunk.Err != nil {
										t.Fatalf("stream chunk error = %v", chunk.Err)
									}
								}
							} else if _, errExecute := executor.Execute(context.Background(), auth, req, opts); errExecute != nil {
								t.Fatalf("Execute() error = %v", errExecute)
							}
						}

						var body []byte
						select {
						case body = <-upstreamRequests:
						default:
							t.Fatal("no upstream request captured")
						}
						maxOutputTokens := gjson.GetBytes(body, "max_output_tokens")
						if authKind == cliproxyauth.AuthKindOAuth && maxOutputTokens.Exists() {
							t.Fatalf("OAuth request contains unsupported max_output_tokens: %s", body)
						}
						if authKind == cliproxyauth.AuthKindAPIKey && maxOutputTokens.Int() != 4096 {
							t.Fatalf("API-key max_output_tokens = %s, want 4096", maxOutputTokens.Raw)
						}
						if got := gjson.GetBytes(body, "model").String(); got != model {
							t.Fatalf("model = %q, want %q", got, model)
						}
						if got := gjson.GetBytes(body, "input.0.role").String(); got != "developer" {
							t.Fatalf("system input role = %q, want developer", got)
						}
						if got := gjson.GetBytes(body, "input.1.content.0.text").String(); got != "hello" {
							t.Fatalf("user input = %q, want hello", got)
						}
						if got := gjson.GetBytes(body, "tools.0.name").String(); got != "lookup" {
							t.Fatalf("tool name = %q, want lookup", got)
						}
						if !gjson.GetBytes(body, "tools.0.strict").Bool() || !gjson.GetBytes(body, "parallel_tool_calls").Bool() {
							t.Fatalf("supported tool configuration was not preserved: %s", body)
						}
						if got := gjson.GetBytes(body, "text.format.type").String(); got != "json_schema" {
							t.Fatalf("structured output type = %q, want json_schema", got)
						}
						if !gjson.GetBytes(body, "text.format.schema.properties.answer").Exists() {
							t.Fatalf("structured output schema was not preserved: %s", body)
						}
						if got := gjson.GetBytes(body, "service_tier").String(); got != "priority" {
							t.Fatalf("service_tier = %q, want priority", got)
						}
					})
				}
			}
		}
	}
}
