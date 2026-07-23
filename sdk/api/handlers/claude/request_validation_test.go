package claude

import "testing"

func TestValidateClaudeMessagesRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "valid string content", payload: `{"model":"claude-fable-5","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "valid block content", payload: `{"model":"claude-fable-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"stop_sequences":["END"]}`},
		{name: "malformed", payload: `{bad`, wantErr: true},
		{name: "not object", payload: `[]`, wantErr: true},
		{name: "missing model", payload: `{"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "missing max tokens", payload: `{"model":"claude-fable-5","messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "zero max tokens", payload: `{"model":"claude-fable-5","max_tokens":0,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "negative max tokens", payload: `{"model":"claude-fable-5","max_tokens":-1,"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "fractional max tokens", payload: `{"model":"claude-fable-5","max_tokens":1.5,"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "fractional underflow max tokens", payload: `{"model":"claude-fable-5","max_tokens":1e-400,"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "negative underflow max tokens", payload: `{"model":"claude-fable-5","max_tokens":-1e-400,"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "integral exponent max tokens", payload: `{"model":"claude-fable-5","max_tokens":1e3,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "missing messages", payload: `{"model":"claude-fable-5","max_tokens":64}`, wantErr: true},
		{name: "bad role", payload: `{"model":"claude-fable-5","max_tokens":64,"messages":[{"role":"root","content":"hello"}]}`, wantErr: true},
		{name: "bad content", payload: `{"model":"claude-fable-5","max_tokens":64,"messages":[{"role":"user","content":42}]}`, wantErr: true},
		{name: "block missing type", payload: `{"model":"claude-fable-5","max_tokens":64,"messages":[{"role":"user","content":[{"text":"hello"}]}]}`, wantErr: true},
		{name: "bad stream", payload: `{"model":"claude-fable-5","max_tokens":64,"stream":"yes","messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "bad stop sequence", payload: `{"model":"claude-fable-5","max_tokens":64,"stop_sequences":[1],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateClaudeMessagesRequest([]byte(test.payload))
			if (err != nil) != test.wantErr {
				t.Fatalf("validate error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateClaudeCountTokensRequest(t *testing.T) {
	t.Parallel()
	if err := validateClaudeCountTokensRequest([]byte(`{"model":"claude-fable-5","messages":[{"role":"user","content":"hello"}]}`)); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if err := validateClaudeCountTokensRequest([]byte(`{"model":"claude-fable-5"}`)); err == nil {
		t.Fatal("missing messages accepted")
	}
	if err := validateClaudeCountTokensRequest([]byte(`{"model":"claude-fable-5","messages":[1]}`)); err == nil {
		t.Fatal("malformed message accepted")
	}
}
