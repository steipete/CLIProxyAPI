package claude

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/tidwall/gjson"
)

type requestValidationError struct {
	message string
}

func (e *requestValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func validateClaudeMessagesRequest(rawJSON []byte) error {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return &requestValidationError{message: "Invalid request: body must be valid JSON"}
	}
	root := gjson.ParseBytes(rawJSON)
	if !root.IsObject() {
		return &requestValidationError{message: "Invalid request: body must be a JSON object"}
	}
	model := root.Get("model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return &requestValidationError{message: "Invalid request: model is required and must be a non-empty string"}
	}
	maxTokens := root.Get("max_tokens")
	// Zero is the current Messages API cache-prewarm contract: populate cache
	// state without generating output. Negative and fractional values are invalid.
	if !maxTokens.Exists() || maxTokens.Type != gjson.Number || !isNonNegativeJSONInteger(maxTokens.Raw) {
		return &requestValidationError{message: "Invalid request: max_tokens is required and must be a non-negative integer"}
	}
	if err := validateClaudeMessageArray(root.Get("messages")); err != nil {
		return err
	}
	if stream := root.Get("stream"); stream.Exists() && stream.Type != gjson.True && stream.Type != gjson.False {
		return &requestValidationError{message: "Invalid request: stream must be a boolean"}
	}
	if stops := root.Get("stop_sequences"); stops.Exists() {
		if !stops.IsArray() {
			return &requestValidationError{message: "Invalid request: stop_sequences must be an array of strings"}
		}
		for index, stop := range stops.Array() {
			if stop.Type != gjson.String {
				return &requestValidationError{message: fmt.Sprintf("Invalid request: stop_sequences.%d must be a string", index)}
			}
		}
	}
	return nil
}

func isNonNegativeJSONInteger(raw string) bool {
	value, ok := new(big.Rat).SetString(strings.TrimSpace(raw))
	return ok && value.Sign() >= 0 && value.IsInt() && value.Num().IsInt64()
}

func validateClaudeCountTokensRequest(rawJSON []byte) error {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return &requestValidationError{message: "Invalid request: body must be valid JSON"}
	}
	root := gjson.ParseBytes(rawJSON)
	if !root.IsObject() {
		return &requestValidationError{message: "Invalid request: body must be a JSON object"}
	}
	model := root.Get("model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return &requestValidationError{message: "Invalid request: model is required and must be a non-empty string"}
	}
	return validateClaudeMessageArray(root.Get("messages"))
}

func validateClaudeMessageArray(messages gjson.Result) error {
	if !messages.IsArray() || len(messages.Array()) == 0 {
		return &requestValidationError{message: "Invalid request: messages is required and must be a non-empty array"}
	}
	for index, message := range messages.Array() {
		if !message.IsObject() {
			return &requestValidationError{message: fmt.Sprintf("Invalid request: messages.%d must be an object", index)}
		}
		role := message.Get("role")
		if role.Type != gjson.String {
			return &requestValidationError{message: fmt.Sprintf("Invalid request: messages.%d.role must be a string", index)}
		}
		// Opus 4.8 supports trusted mid-conversation system messages. Model/provider
		// capability validation happens after routing; this layer only validates shape.
		switch role.String() {
		case "user", "assistant", "system":
		default:
			return &requestValidationError{message: fmt.Sprintf("Invalid request: messages.%d.role must be user, assistant, or system", index)}
		}
		content := message.Get("content")
		if content.Type != gjson.String && !content.IsArray() {
			return &requestValidationError{message: fmt.Sprintf("Invalid request: messages.%d.content must be a string or array", index)}
		}
		if content.IsArray() {
			for blockIndex, block := range content.Array() {
				if !block.IsObject() || block.Get("type").Type != gjson.String || strings.TrimSpace(block.Get("type").String()) == "" {
					return &requestValidationError{message: fmt.Sprintf("Invalid request: messages.%d.content.%d must be an object with a type", index, blockIndex)}
				}
			}
		}
	}
	return nil
}
