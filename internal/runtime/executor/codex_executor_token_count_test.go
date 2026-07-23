package executor

import (
	"errors"
	"net/http"
	"testing"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
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
