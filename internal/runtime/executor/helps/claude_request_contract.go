package helps

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	"github.com/tidwall/gjson"
)

// ValidateClaudeRequestForCodex rejects Anthropic features that the Codex
// Responses boundary cannot represent without silently changing semantics.
func ValidateClaudeRequestForCodex(rawJSON []byte) error {
	root := gjson.ParseBytes(rawJSON)
	messages := root.Get("messages")
	if messages.IsArray() {
		for messageIndex, message := range messages.Array() {
			content := message.Get("content")
			if !content.IsArray() {
				continue
			}
			for blockIndex, block := range content.Array() {
				blockType := block.Get("type").String()
				switch blockType {
				case "text", "thinking", "redacted_thinking", "tool_use", "compaction", "fallback":
				case "server_tool_use":
					if block.Get("name").String() != "web_search" {
						return requestContractError("messages.%d.content.%d: unsupported Claude server tool %q for Codex", messageIndex, blockIndex, block.Get("name").String())
					}
				case "web_search_tool_result":
					if claudeWebSearchHistoryHasEncryptedContent(block.Get("content")) {
						return requestContractError("messages.%d.content.%d: encrypted web search history cannot be replayed through Codex", messageIndex, blockIndex)
					}
				case "search_result":
					if err := validateClaudeSearchResult(block); err != nil {
						return requestContractError("messages.%d.content.%d: %v", messageIndex, blockIndex, err)
					}
				case "image":
					if err := validateClaudeMediaSource(block.Get("source"), true); err != nil {
						return requestContractError("messages.%d.content.%d: %v", messageIndex, blockIndex, err)
					}
				case "document":
					if block.Get("citations.enabled").Bool() {
						return requestContractError("messages.%d.content.%d: document citations are not supported by the Codex boundary", messageIndex, blockIndex)
					}
					if err := validateClaudeMediaSource(block.Get("source"), false); err != nil {
						return requestContractError("messages.%d.content.%d: %v", messageIndex, blockIndex, err)
					}
				case "tool_result":
					if err := validateClaudeToolResultContent(block.Get("content")); err != nil {
						return requestContractError("messages.%d.content.%d: %v", messageIndex, blockIndex, err)
					}
				default:
					return requestContractError("messages.%d.content.%d: unsupported Claude content block type %q for Codex", messageIndex, blockIndex, blockType)
				}
			}
		}
	}

	tools := root.Get("tools")
	if tools.IsArray() {
		for index, tool := range tools.Array() {
			toolType := strings.TrimSpace(tool.Get("type").String())
			switch toolType {
			case "", "custom", "function":
				if tool.Get("defer_loading").Bool() {
					return requestContractError("tools.%d.defer_loading is not supported by the Codex boundary", index)
				}
				// Codex function tools are always model-invoked, so a singleton
				// "direct" restriction is already the Codex behavior and is
				// losslessly representable; anything else is not.
				if callers := tool.Get("allowed_callers"); callers.Exists() && len(callers.Array()) > 0 {
					values := callers.Array()
					if len(values) != 1 || strings.TrimSpace(values[0].String()) != "direct" {
						return requestContractError("tools.%d.allowed_callers is not supported by the Codex boundary", index)
					}
				}
				if tool.Get("strict").Bool() {
					schema := tool.Get("input_schema")
					if !codexStrictSchemaRootIsObject(schema) {
						return requestContractError("tools.%d.input_schema must have an object root for Codex strict tools", index)
					}
					if _, err := translatorcommon.NormalizeCodexStrictSchema(schema.Raw); err != nil {
						return requestContractError("tools.%d.input_schema: %v", index, err)
					}
				}
			case "web_search_20250305", "web_search_20260209", "web_search_20260318":
				if tool.Get("max_uses").Exists() {
					return requestContractError("tools.%d.max_uses is not supported by the Codex web search boundary", index)
				}
				if err := validateClaudeWebSearchCallers(toolType, tool.Get("allowed_callers")); err != nil {
					return requestContractError("tools.%d.allowed_callers: %v", index, err)
				}
				if blocked := tool.Get("blocked_domains"); blocked.Exists() && len(blocked.Array()) > 0 {
					return requestContractError("tools.%d.blocked_domains is not supported by the Codex web search boundary", index)
				}
				if inclusion := strings.TrimSpace(tool.Get("response_inclusion").String()); inclusion != "" && inclusion != "full" {
					return requestContractError("tools.%d.response_inclusion %q is not supported by the Codex web search boundary", index, inclusion)
				}
			default:
				return requestContractError("tools.%d: unsupported Claude tool type %q for Codex", index, toolType)
			}
		}
	}

	if format := root.Get("output_config.format"); format.Exists() {
		if format.Get("type").String() != "json_schema" || !codexStrictSchemaRootIsObject(format.Get("schema")) {
			return requestContractError("output_config.format must be a json_schema with an object root schema for Codex")
		}
		if _, err := translatorcommon.NormalizeCodexStrictSchema(format.Get("schema").Raw); err != nil {
			return requestContractError("output_config.format.schema: %v", err)
		}
	}

	model := util.CanonicalClaudeModelID(root.Get("model").String())
	thinkingType := strings.ToLower(strings.TrimSpace(root.Get("thinking.type").String()))
	alwaysThinking := model == "claude-fable-5" || model == "claude-mythos-5"
	manualThinkingRemoved := alwaysThinking || model == "claude-opus-4-7" || model == "claude-opus-4-8" || model == "claude-sonnet-5"
	if alwaysThinking && thinkingType == "disabled" {
		return requestContractError("thinking.type %q is not supported by %s", thinkingType, root.Get("model").String())
	}
	if manualThinkingRemoved && thinkingType == "enabled" {
		return requestContractError("thinking.type %q is not supported by %s", thinkingType, root.Get("model").String())
	}
	if model == "claude-mythos-preview" && thinkingType == "disabled" {
		return requestContractError("thinking.type %q is not supported by %s", thinkingType, root.Get("model").String())
	}
	return nil
}

func codexStrictSchemaRootIsObject(schema gjson.Result) bool {
	return schema.IsObject() && schema.Get("type").Type == gjson.String && schema.Get("type").String() == "object"
}

func claudeWebSearchHistoryHasEncryptedContent(content gjson.Result) bool {
	if !content.IsArray() {
		return false
	}
	for _, result := range content.Array() {
		if strings.TrimSpace(result.Get("encrypted_content").String()) != "" {
			return true
		}
	}
	return false
}

func validateClaudeToolResultContent(content gjson.Result) error {
	if !content.IsArray() {
		return nil
	}
	for index, block := range content.Array() {
		switch block.Get("type").String() {
		case "text":
		case "search_result":
			if err := validateClaudeSearchResult(block); err != nil {
				return fmt.Errorf("tool_result content.%d: %w", index, err)
			}
		case "image":
			if err := validateClaudeMediaSource(block.Get("source"), true); err != nil {
				return fmt.Errorf("tool_result content.%d: %w", index, err)
			}
		case "document":
			if block.Get("citations.enabled").Bool() {
				return fmt.Errorf("tool_result content.%d: document citations are not supported by the Codex boundary", index)
			}
			if err := validateClaudeMediaSource(block.Get("source"), false); err != nil {
				return fmt.Errorf("tool_result content.%d: %w", index, err)
			}
		default:
			return fmt.Errorf("tool_result content.%d: unsupported block type %q", index, block.Get("type").String())
		}
	}
	return nil
}

func validateClaudeMediaSource(source gjson.Result, image bool) error {
	if !source.IsObject() {
		return fmt.Errorf("media source must be an object")
	}
	sourceType := source.Get("type").String()
	switch sourceType {
	case "url":
		rawURL := strings.TrimSpace(source.Get("url").String())
		parsedURL, err := url.Parse(rawURL)
		if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return fmt.Errorf("url media source requires an absolute HTTP(S) url")
		}
		return nil
	case "base64":
		mediaType := strings.TrimSpace(source.Get("media_type").String())
		if mediaType == "" {
			mediaType = strings.TrimSpace(source.Get("mime_type").String())
		}
		if mediaType == "" {
			return fmt.Errorf("base64 media source requires a media_type")
		}
		if !isSupportedClaudeMediaType(mediaType, image) {
			kind := "document"
			if image {
				kind = "image"
			}
			return fmt.Errorf("unsupported %s media type %q", kind, mediaType)
		}
		data := strings.TrimSpace(source.Get("data").String())
		if data == "" {
			data = strings.TrimSpace(source.Get("base64").String())
		}
		if data == "" {
			return fmt.Errorf("base64 media source requires data")
		}
		if _, err := base64.StdEncoding.DecodeString(data); err != nil {
			if _, rawErr := base64.RawStdEncoding.DecodeString(data); rawErr != nil {
				return fmt.Errorf("base64 media source contains invalid data")
			}
		}
		return nil
	case "text":
		if !image {
			return nil
		}
		return fmt.Errorf("unsupported image source type %q", sourceType)
	case "content":
		if image {
			return fmt.Errorf("unsupported image source type %q", sourceType)
		}
		return validateClaudeContentDocument(source.Get("content"))
	case "file":
		kind := "document"
		if image {
			kind = "image"
		}
		return fmt.Errorf("%s file_id sources cannot be resolved by the Codex boundary", kind)
	default:
		return fmt.Errorf("unsupported media source type %q", sourceType)
	}
}

func isSupportedClaudeMediaType(mediaType string, image bool) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if image {
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
			return true
		default:
			return false
		}
	}
	return mediaType == "application/pdf"
}

func validateClaudeWebSearchCallers(toolType string, callers gjson.Result) error {
	if !callers.Exists() {
		if toolType == "web_search_20250305" {
			return nil
		}
		return fmt.Errorf("%s defaults to unsupported code-execution calling; set [\"direct\"]", toolType)
	}
	if !callers.IsArray() {
		return fmt.Errorf("must be [\"direct\"]")
	}
	values := callers.Array()
	if len(values) != 1 || values[0].Type != gjson.String || values[0].String() != "direct" {
		return fmt.Errorf("only [\"direct\"] is supported by the Codex boundary")
	}
	return nil
}

func validateClaudeSearchResult(block gjson.Result) error {
	if block.Get("citations.enabled").Bool() {
		return fmt.Errorf("search result citations are not supported by the Codex boundary")
	}
	if strings.TrimSpace(block.Get("source").String()) == "" {
		return fmt.Errorf("search result source is required")
	}
	if strings.TrimSpace(block.Get("title").String()) == "" {
		return fmt.Errorf("search result title is required")
	}
	content := block.Get("content")
	if !content.IsArray() || len(content.Array()) == 0 {
		return fmt.Errorf("search result content must be a non-empty array of text blocks")
	}
	for index, part := range content.Array() {
		if part.Get("type").String() != "text" {
			return fmt.Errorf("search result content.%d: unsupported block type %q", index, part.Get("type").String())
		}
		if strings.TrimSpace(part.Get("text").String()) == "" {
			return fmt.Errorf("search result content.%d: text must be non-empty", index)
		}
	}
	return nil
}

func validateClaudeContentDocument(content gjson.Result) error {
	if content.Type == gjson.String {
		return nil
	}
	if !content.IsArray() {
		return fmt.Errorf("content document source must contain text or image blocks")
	}
	for index, block := range content.Array() {
		switch block.Get("type").String() {
		case "text":
		case "image":
			if err := validateClaudeMediaSource(block.Get("source"), true); err != nil {
				return fmt.Errorf("content.%d: %w", index, err)
			}
		default:
			return fmt.Errorf("content.%d: unsupported block type %q", index, block.Get("type").String())
		}
	}
	return nil
}

func requestContractError(format string, args ...any) error {
	return &cliproxyexecutor.RequestError{
		HTTPStatus: http.StatusBadRequest,
		Code:       "invalid_request_error",
		Message:    fmt.Sprintf(format, args...),
	}
}
