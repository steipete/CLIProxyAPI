package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tiktoken-go/tokenizer"
)

func (e *CodexExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	from := opts.SourceFormat
	if sourceFormatEqual(from, sdktranslator.FormatClaude) {
		if errValidate := helps.ValidateClaudeRequestForCodex(req.Payload); errValidate != nil {
			return cliproxyexecutor.Response{}, errValidate
		}
	}
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("codex")
	body := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, req.Payload, false, helps.APIKeyModelIsCompat(req))

	body, err := helps.ApplyRequestThinking(body, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	body = helps.SetStringIfDifferent(body, "model", baseModel)
	body = normalizeCodexInstructions(body)

	fieldActive := func(path string) bool {
		value := gjson.GetBytes(body, path)
		return value.Exists() && value.Type != gjson.Null
	}
	stateful := fieldActive("previous_response_id") || fieldActive("conversation")
	if fieldActive("prompt") {
		return cliproxyexecutor.Response{}, &cliproxyexecutor.RequestError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "invalid_request_error",
			Message:    "routed token counting does not support reusable prompt templates",
		}
	}

	var count int64
	if stateful || codexInputHasMediaParts(body) {
		apiKey, baseURL := codexCreds(auth)
		if strings.TrimSpace(baseURL) == "" {
			return cliproxyexecutor.Response{}, errCodexTokenCountMediaUnsupported()
		}
		remoteCount, errRemote := countCodexInputTokensRemote(ctx, e.cfg, auth, apiKey, baseURL, body)
		if errRemote != nil {
			return cliproxyexecutor.Response{}, errRemote
		}
		count = remoteCount
	} else {
		body, _ = sjson.DeleteBytes(body, "previous_response_id")
		body, _ = sjson.DeleteBytes(body, "generate")
		body, _ = sjson.DeleteBytes(body, "prompt_cache_retention")
		body, _ = sjson.DeleteBytes(body, "safety_identifier")
		body, _ = sjson.DeleteBytes(body, "stream_options")
		body = helps.SetBoolIfDifferent(body, "stream", false)
		enc, errEnc := tokenizerForCodexModel(baseModel)
		if errEnc != nil {
			return cliproxyexecutor.Response{}, fmt.Errorf("codex executor: tokenizer init failed: %w", errEnc)
		}
		localCount, errCount := countCodexInputTokens(enc, body)
		if errCount != nil {
			return cliproxyexecutor.Response{}, fmt.Errorf("codex executor: token counting failed: %w", errCount)
		}
		count = localCount
	}

	usageJSON := fmt.Sprintf(`{"response":{"usage":{"input_tokens":%d,"output_tokens":0,"total_tokens":%d}}}`, count, count)
	translated := sdktranslator.TranslateTokenCount(ctx, to, responseFormat, count, []byte(usageJSON))
	return cliproxyexecutor.Response{Payload: translated}, nil
}

func tokenizerForCodexModel(model string) (tokenizer.Codec, error) {
	sanitized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case sanitized == "":
		return tokenizer.Get(tokenizer.Cl100kBase)
	case strings.HasPrefix(sanitized, "gpt-5"):
		return tokenizer.ForModel(tokenizer.GPT5)
	case strings.HasPrefix(sanitized, "gpt-4.1"):
		return tokenizer.ForModel(tokenizer.GPT41)
	case strings.HasPrefix(sanitized, "gpt-4o"):
		return tokenizer.ForModel(tokenizer.GPT4o)
	case strings.HasPrefix(sanitized, "gpt-4"):
		return tokenizer.ForModel(tokenizer.GPT4)
	case strings.HasPrefix(sanitized, "gpt-3.5"), strings.HasPrefix(sanitized, "gpt-3"):
		return tokenizer.ForModel(tokenizer.GPT35Turbo)
	default:
		return tokenizer.Get(tokenizer.Cl100kBase)
	}
}

func errCodexTokenCountMediaUnsupported() *cliproxyexecutor.RequestError {
	return &cliproxyexecutor.RequestError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "invalid_request_error",
		Message:    "routed token counting does not support image or document inputs for this credential",
	}
}

func codexInputItemIsMessage(item gjson.Result) bool {
	switch item.Get("type").String() {
	case "message":
		return true
	case "":
		return item.Get("role").Exists() && item.Get("content").Exists()
	}
	return false
}

func codexInputHasMediaParts(body []byte) bool {
	found := false
	scanParts := func(content gjson.Result) {
		if !content.IsArray() || found {
			return
		}
		content.ForEach(func(_, part gjson.Result) bool {
			switch part.Get("type").String() {
			case "input_image", "input_file":
				found = true
				return false
			}
			return true
		})
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	input.ForEach(func(_, item gjson.Result) bool {
		scanParts(item.Get("content"))
		scanParts(item.Get("output"))
		if item.Get("output.type").String() == "computer_screenshot" {
			found = true
		}
		if item.Get("type").String() == "image_generation_call" && item.Get("result").String() != "" {
			found = true
		}
		if outputs := item.Get("outputs"); outputs.IsArray() {
			outputs.ForEach(func(_, out gjson.Result) bool {
				if out.Get("type").String() == "image" {
					found = true
					return false
				}
				return true
			})
		}
		return !found
	})
	return found
}

func countCodexInputTokensRemote(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, apiKey, baseURL string, body []byte) (int64, error) {
	payload := []byte(`{}`)
	for _, path := range []string{"model", "instructions", "input", "tools", "tool_choice", "parallel_tool_calls", "conversation", "previous_response_id", "reasoning", "text", "truncation", "personality"} {
		if value := gjson.GetBytes(body, path); value.Exists() {
			payload, _ = sjson.SetRawBytes(payload, path, []byte(value.Raw))
		}
	}
	url := strings.TrimSuffix(baseURL, "/") + "/responses/input_tokens"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("codex executor: token count request failed: %w", err)
	}
	applyCodexHeaders(httpReq, auth, apiKey, false, cfg)
	httpClient := helps.NewUtlsHTTPClient(ctx, cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("codex executor: token count request failed: %w", err)
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("codex executor: close token count response body error: %v", errClose)
		}
	}()
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return 0, fmt.Errorf("codex executor: token count response read failed: %w", err)
	}
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return 0, newCodexStatusErr(httpResp.StatusCode, data)
	}
	tokens := gjson.GetBytes(data, "input_tokens")
	if !tokens.Exists() {
		return 0, fmt.Errorf("codex executor: token count response missing input_tokens")
	}
	return tokens.Int(), nil
}

func countCodexInputTokens(enc tokenizer.Codec, body []byte) (int64, error) {
	if enc == nil {
		return 0, fmt.Errorf("encoder is nil")
	}
	if len(body) == 0 {
		return 0, nil
	}

	root := gjson.ParseBytes(body)
	var segments []string
	var structuralTokens int64
	appendContentParts := func(content gjson.Result) error {
		if !content.IsArray() {
			return nil
		}
		parts := content.Array()
		for i := range parts {
			part := parts[i]
			switch part.Get("type").String() {
			case "input_image", "input_file":
				return errCodexTokenCountMediaUnsupported()
			default:
				if text := strings.TrimSpace(part.Get("text").String()); text != "" {
					segments = append(segments, text)
				}
			}
		}
		return nil
	}

	if inst := strings.TrimSpace(root.Get("instructions").String()); inst != "" {
		segments = append(segments, inst)
	}

	inputItems := root.Get("input")
	if inputItems.IsArray() {
		arr := inputItems.Array()
		for i := range arr {
			item := arr[i]
			structuralTokens += 4
			if codexInputItemIsMessage(item) {
				content := item.Get("content")
				if content.Type == gjson.String {
					if text := strings.TrimSpace(content.String()); text != "" {
						segments = append(segments, text)
					}
					continue
				}
				if err := appendContentParts(content); err != nil {
					return 0, err
				}
				continue
			}
			switch item.Get("type").String() {
			case "function_call":
				if name := strings.TrimSpace(item.Get("name").String()); name != "" {
					segments = append(segments, name)
				}
				if args := strings.TrimSpace(item.Get("arguments").String()); args != "" {
					segments = append(segments, args)
				}
			case "function_call_output":
				output := item.Get("output")
				if output.IsArray() {
					if err := appendContentParts(output); err != nil {
						return 0, err
					}
				} else if out := strings.TrimSpace(output.String()); out != "" {
					segments = append(segments, out)
				}
			default:
				if text := strings.TrimSpace(item.Get("text").String()); text != "" {
					segments = append(segments, text)
				}
			}
		}
	}

	tools := root.Get("tools")
	if tools.IsArray() {
		tarr := tools.Array()
		for i := range tarr {
			tool := tarr[i]
			structuralTokens += 8
			if name := strings.TrimSpace(tool.Get("name").String()); name != "" {
				segments = append(segments, name)
			}
			if desc := strings.TrimSpace(tool.Get("description").String()); desc != "" {
				segments = append(segments, desc)
			}
			if params := tool.Get("parameters"); params.Exists() {
				val := params.Raw
				if params.Type == gjson.String {
					val = params.String()
				}
				if trimmed := strings.TrimSpace(val); trimmed != "" {
					segments = append(segments, trimmed)
				}
			}
		}
	}

	textFormat := root.Get("text.format")
	if textFormat.Exists() {
		if name := strings.TrimSpace(textFormat.Get("name").String()); name != "" {
			segments = append(segments, name)
		}
		if schema := textFormat.Get("schema"); schema.Exists() {
			val := schema.Raw
			if schema.Type == gjson.String {
				val = schema.String()
			}
			if trimmed := strings.TrimSpace(val); trimmed != "" {
				segments = append(segments, trimmed)
			}
		}
	}

	text := strings.Join(segments, "\n")
	textTokens := int64(0)
	if text != "" {
		count, err := enc.Count(text)
		if err != nil {
			return 0, err
		}
		textTokens = int64(count)
	}
	return textTokens + structuralTokens, nil
}
