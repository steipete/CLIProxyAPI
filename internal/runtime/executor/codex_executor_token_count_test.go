package executor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
	"github.com/tiktoken-go/tokenizer"
)

func TestCountCodexInputTokensRejectsMedia(t *testing.T) {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	payloads := [][]byte{
		[]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"describe"}]}]}`),
		[]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:application/pdf;base64,QUJDREVGRw=="},{"type":"input_text","text":"describe"}]}]}`),
		[]byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_url":"https://example.com/a.pdf"},{"type":"input_text","text":"describe"}]}]}`),
		[]byte(`{"input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"tool result"}]}]}`),
		[]byte(`{"input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_file","file_url":"https://example.com/a.pdf"},{"type":"input_text","text":"tool result"}]}]}`),
	}
	for i, payload := range payloads {
		_, errCount := countCodexInputTokens(enc, payload)
		var requestErr *cliproxyexecutor.RequestError
		if !errors.As(errCount, &requestErr) || requestErr.StatusCode() != http.StatusBadRequest {
			t.Fatalf("payload %d: error = %v, want request-scoped 400", i, errCount)
		}
	}
}

func TestCountCodexInputTokensCountsTextAndStructure(t *testing.T) {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	count, errCount := countCodexInputTokens(enc, []byte(`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"describe"}]}]}`))
	if errCount != nil {
		t.Fatalf("count: %v", errCount)
	}
	if count <= 0 {
		t.Fatalf("count = %d, want positive", count)
	}
}

func TestCodexInputHasMediaParts(t *testing.T) {
	cases := []struct {
		payload string
		want    bool
	}{
		{`{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, false},
		{`{"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, true},
		{`{"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_url":"https://example.com/a.pdf"}]}]}`, true},
		{`{"input":[{"type":"function_call_output","call_id":"c","output":[{"type":"input_file","file_data":"data:application/pdf;base64,AA=="}]}]}`, true},
		{`{"input":[{"type":"function_call_output","call_id":"c","output":"plain text"}]}`, false},
		{`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}]}`, true},
		{`{"input":[{"role":"user","content":"plain string"}]}`, false},
		{`{"input":[{"type":"computer_call_output","call_id":"c","output":{"type":"computer_screenshot","image_url":"data:image/png;base64,AA=="}}]}`, true},
		{`{"input":[{"type":"custom_tool_call_output","call_id":"c","output":[{"type":"input_file","file_data":"data:application/pdf;base64,AA=="}]}]}`, true},
		{`{"input":[{"type":"image_generation_call","id":"ig_1","result":"aGVsbG8="}]}`, true},
		{`{"input":[{"type":"code_interpreter_call","id":"ci_1","outputs":[{"type":"image","url":"https://example.com/plot.png"}]}]}`, true},
		{`{"input":[{"type":"code_interpreter_call","id":"ci_1","outputs":[{"type":"logs","logs":"ok"}]}]}`, false},
	}
	for i, tc := range cases {
		if got := codexInputHasMediaParts([]byte(tc.payload)); got != tc.want {
			t.Fatalf("case %d: got %v, want %v", i, got, tc.want)
		}
	}
}

func TestCountCodexInputTokensRemote(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"response.input_tokens","input_tokens":4242}`))
	}))
	defer server.Close()

	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"stream_options":{"include_usage":true},"max_output_tokens":16,"service_tier":"priority","temperature":1,"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]}],"tools":[{"type":"function","name":"probe","parameters":{"type":"object","properties":{},"required":[],"additionalProperties":false},"strict":true}]}`)
	count, err := countCodexInputTokensRemote(context.Background(), nil, nil, "test-key", server.URL, body)
	if err != nil {
		t.Fatalf("remote count: %v", err)
	}
	if count != 4242 {
		t.Fatalf("count = %d, want 4242", count)
	}
	if gotPath != "/responses/input_tokens" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	for _, path := range []string{"stream", "stream_options", "max_output_tokens", "service_tier", "temperature"} {
		if gjson.GetBytes(gotBody, path).Exists() {
			t.Fatalf("create-only field %q must not reach the count endpoint: %s", path, gotBody)
		}
	}
	if gjson.GetBytes(gotBody, "input.0.content.0.type").String() != "input_image" {
		t.Fatalf("input not forwarded: %s", gotBody)
	}
	if gjson.GetBytes(gotBody, "tools.0.name").String() != "probe" {
		t.Fatalf("tools must be forwarded for accurate counts: %s", gotBody)
	}
}

func TestCountCodexInputTokensRemoteUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad schema"}}`))
	}))
	defer server.Close()
	_, err := countCodexInputTokensRemote(context.Background(), nil, nil, "test-key", server.URL, []byte(`{"model":"gpt-5.6-sol","input":[]}`))
	if err == nil {
		t.Fatal("want upstream error passthrough")
	}
}

func TestCountCodexInputTokensCountsTypelessMessages(t *testing.T) {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	count, errCount := countCodexInputTokens(enc, []byte(`{"input":[{"role":"user","content":"a longer string body that should be tokenized"},{"role":"user","content":[{"type":"input_text","text":"and array parts too"}]}]}`))
	if errCount != nil {
		t.Fatalf("count: %v", errCount)
	}
	if count < 12 {
		t.Fatalf("count = %d, want type-less message content tokenized", count)
	}
}

func TestCountTokensNullOptionalFieldsAreInactive(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-sol","prompt":null,"conversation":null,"previous_response_id":null,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	for _, path := range []string{"prompt", "conversation", "previous_response_id"} {
		value := gjson.GetBytes(body, path)
		if !value.Exists() || value.Type != gjson.Null {
			t.Fatalf("test payload must carry explicit null for %q", path)
		}
	}
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	// Null-inactive fields must keep this on the local path and count fine.
	count, errCount := countCodexInputTokens(enc, body)
	if errCount != nil {
		t.Fatalf("count: %v", errCount)
	}
	if count <= 0 {
		t.Fatalf("count = %d, want positive", count)
	}
}
