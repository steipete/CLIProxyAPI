// Package claude provides request translation functionality for Claude Code API compatibility.
// It handles parsing and transforming Claude Code API requests into the internal client format,
// extracting model information, system instructions, message contents, and tool declarations.
// The package also performs JSON data cleaning and transformation to ensure compatibility
// between Claude Code API format and the internal client's expected format.
package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	sigcompat "github.com/router-for-me/CLIProxyAPI/v7/internal/signature"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	translatorcommon "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/common"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// ConvertClaudeRequestToCodex parses and transforms a Claude Code API request into the internal client format.
// It extracts the model name, system instruction, message contents, and tool declarations
// from the raw JSON request and returns them in the format expected by the internal client.
// The function performs the following transformations:
// 1. Sets up a template with the model name and empty instructions field
// 2. Processes system messages and converts them to developer input content
// 3. Transforms message contents (text, image, tool_use, tool_result) to appropriate formats
// 4. Converts tools declarations to the expected format
// 5. Adds additional configuration parameters for the Codex API
// 6. Maps Claude thinking configuration to Codex reasoning settings
//
// Parameters:
//   - modelName: The name of the model to use for the request
//   - rawJSON: The raw JSON request data from the Claude Code API
//   - stream: A boolean indicating if the request is for a streaming response (unused in current implementation)
//
// Returns:
//   - []byte: The transformed request data in internal client format
func ConvertClaudeRequestToCodex(modelName string, inputRawJSON []byte, _ bool) []byte {
	rawJSON := inputRawJSON

	template := []byte(`{"model":"","instructions":"","input":[]}`)

	rootResult := gjson.ParseBytes(rawJSON)
	toolNameMap := buildReverseMapFromClaudeOriginalToShort(rawJSON)
	template, _ = sjson.SetBytes(template, "model", modelName)
	inputItems := translatorcommon.NewRawArrayItems(rootResult.Get("messages.#").Int())

	// Process system messages and convert them to input content format.
	systemsResult := rootResult.Get("system")
	if systemsResult.Exists() {
		contentItems := make([][]byte, 0, 2)

		appendSystemText := func(text string) {
			if text == "" || util.IsClaudeCodeAttributionSystemText(text) {
				return
			}

			content := []byte(`{"type":"input_text","text":""}`)
			content, _ = sjson.SetBytes(content, "text", text)
			contentItems = append(contentItems, content)
		}

		if systemsResult.Type == gjson.String {
			appendSystemText(systemsResult.String())
		} else if systemsResult.IsArray() {
			systemResults := systemsResult.Array()
			for i := 0; i < len(systemResults); i++ {
				systemResult := systemResults[i]
				if systemResult.Get("type").String() == "text" {
					appendSystemText(systemResult.Get("text").String())
				}
			}
		}

		if len(contentItems) > 0 {
			message := []byte(`{"type":"message","role":"developer"}`)
			message, _ = sjson.SetRawBytes(message, "content", translatorcommon.JoinRawArray(contentItems))
			inputItems = append(inputItems, message)
		}
	}

	// Preserve top-level system instructions when a compaction block replaces
	// earlier message history.
	systemInputCount := len(inputItems)

	// Process messages and transform their contents to appropriate formats.
	messagesResult := rootResult.Get("messages")
	if messagesResult.IsArray() {
		messageResults := messagesResult.Array()

		for i := 0; i < len(messageResults); i++ {
			messageResult := messageResults[i]
			messageRole := messageResult.Get("role").String()
			if messageRole == "system" {
				if reminderText, ok := translatorcommon.ClaudeMessageSystemReminderText(messageResult.Get("content")); ok {
					message := []byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}`)
					message, _ = sjson.SetBytes(message, "content.0.text", reminderText)
					inputItems = append(inputItems, message)
				}
				continue
			}

			messageContentsResult := messageResult.Get("content")
			contentItems := make([][]byte, 0, 4)

			flushMessage := func() {
				if len(contentItems) > 0 {
					message := []byte(`{"type":"message","role":""}`)
					message, _ = sjson.SetBytes(message, "role", messageRole)
					message, _ = sjson.SetRawBytes(message, "content", translatorcommon.JoinRawArray(contentItems))
					inputItems = append(inputItems, message)
					contentItems = contentItems[:0]
				}
			}

			appendTextContent := func(text string) {
				partType := "input_text"
				if messageRole == "assistant" {
					partType = "output_text"
				}
				content := []byte(`{"type":"","text":""}`)
				content, _ = sjson.SetBytes(content, "type", partType)
				content, _ = sjson.SetBytes(content, "text", text)
				contentItems = append(contentItems, content)
			}

			appendImageContent := func(imageURL string) {
				content := []byte(`{"type":"input_image","image_url":""}`)
				content, _ = sjson.SetBytes(content, "image_url", imageURL)
				contentItems = append(contentItems, content)
			}

			appendDocumentContent := func(field, value, filename string) {
				content := []byte(`{"type":"input_file"}`)
				content, _ = sjson.SetBytes(content, field, value)
				if field == "file_data" {
					filename = codexInlineDocumentFilename(value, filename)
				}
				if filename != "" {
					content, _ = sjson.SetBytes(content, "filename", filename)
				}
				contentItems = append(contentItems, content)
			}

			appendReasoningContent := func(part gjson.Result) {
				if messageRole != "assistant" {
					return
				}

				rawSignature := part.Get("signature").String()
				signature, ok := sigcompat.CompatibleSignatureForProvider(sigcompat.SignatureProviderGPT, rawSignature)
				if !ok {
					if !codexClaudeTargetAcceptsGrokSignature(modelName) {
						return
					}
					if _, err := sigcompat.InspectGrokEncryptedContent(rawSignature); err != nil {
						return
					}
					signature = rawSignature
				}

				flushMessage()
				reasoningItem := []byte(`{"type":"reasoning","summary":[],"content":null}`)
				reasoningItem, _ = sjson.SetBytes(reasoningItem, "encrypted_content", signature)
				inputItems = append(inputItems, reasoningItem)
			}

			if messageContentsResult.IsArray() {
				messageContentResults := messageContentsResult.Array()
				for j := 0; j < len(messageContentResults); j++ {
					messageContentResult := messageContentResults[j]
					contentType := messageContentResult.Get("type").String()

					switch contentType {
					case "text":
						appendTextContent(messageContentResult.Get("text").String())
					case "thinking":
						appendReasoningContent(messageContentResult)
					case "redacted_thinking", "fallback":
						// These are model-internal history markers. They must be accepted on
						// replay but cannot be translated into a valid GPT reasoning signature.
					case "compaction":
						if summary := firstNonEmptyClaudeString(messageContentResult, "summary", "content", "text"); summary != "" {
							contentItems = contentItems[:0]
							inputItems = inputItems[:systemInputCount]
							appendTextContent(summary)
						}
					case "server_tool_use":
						if messageContentResult.Get("name").String() == "web_search" {
							query := strings.TrimSpace(messageContentResult.Get("input.query").String())
							if query != "" {
								appendTextContent("Web search requested: " + query)
							}
						}
					case "web_search_tool_result":
						if text := claudeWebSearchResultHistoryText(messageContentResult); text != "" {
							appendTextContent(text)
						}
					case "search_result":
						if text := claudeSearchResultText(messageContentResult); text != "" {
							appendTextContent(text)
						}
					case "image":
						sourceResult := messageContentResult.Get("source")
						if sourceResult.Get("type").String() == "url" {
							if imageURL := sourceResult.Get("url").String(); imageURL != "" {
								appendImageContent(imageURL)
							}
							break
						}
						if sourceResult.Exists() {
							if data := claudeMediaData(sourceResult); data != "" {
								appendImageContent(fmt.Sprintf("data:%s;base64,%s", claudeMediaType(sourceResult), data))
							}
						}
					case "document":
						appendClaudeDocumentContext(messageContentResult.Get("context"), appendTextContent)
						sourceResult := messageContentResult.Get("source")
						switch sourceResult.Get("type").String() {
						case "url":
							appendDocumentContent("file_url", sourceResult.Get("url").String(), messageContentResult.Get("title").String())
							break
						case "text":
							appendClaudeDocumentTitle(messageContentResult.Get("title").String(), appendTextContent)
							appendTextContent(sourceResult.Get("data").String())
							break
						case "content":
							appendClaudeDocumentTitle(messageContentResult.Get("title").String(), appendTextContent)
							appendClaudeContentDocumentSource(sourceResult.Get("content"), appendTextContent, appendImageContent)
							break
						}
						if sourceResult.Get("type").String() != "base64" {
							break
						}
						if data := claudeMediaData(sourceResult); data != "" {
							appendDocumentContent("file_data", fmt.Sprintf("data:%s;base64,%s", claudeMediaType(sourceResult), data), messageContentResult.Get("title").String())
						}
					case "tool_use":
						flushMessage()
						functionCallMessage := []byte(`{"type":"function_call"}`)
						functionCallMessage, _ = sjson.SetBytes(functionCallMessage, "call_id", shortenCodexCallIDIfNeeded(messageContentResult.Get("id").String()))
						{
							name := messageContentResult.Get("name").String()
							if short, ok := toolNameMap[name]; ok {
								name = short
							} else {
								name = shortenNameIfNeeded(name)
							}
							functionCallMessage, _ = sjson.SetBytes(functionCallMessage, "name", name)
						}
						functionCallMessage, _ = sjson.SetBytes(functionCallMessage, "arguments", messageContentResult.Get("input").Raw)
						inputItems = append(inputItems, functionCallMessage)
					case "tool_result":
						flushMessage()
						functionCallOutputMessage := []byte(`{"type":"function_call_output"}`)
						functionCallOutputMessage, _ = sjson.SetBytes(functionCallOutputMessage, "call_id", shortenCodexCallIDIfNeeded(messageContentResult.Get("tool_use_id").String()))
						// Responses `status` is item lifecycle, not error semantics: a failed
						// tool call still completed, so only the injected failure text below
						// carries Anthropic's is_error signal.
						isError := messageContentResult.Get("is_error").Bool()

						contentResult := messageContentResult.Get("content")
						if contentResult.IsArray() {
							contentResults := contentResult.Array()
							toolResultContentItems := make([][]byte, 0, len(contentResults)+1)
							if isError {
								toolResultContentItems = append(toolResultContentItems, []byte(`{"type":"input_text","text":"Tool execution failed."}`))
							}
							for k := 0; k < len(contentResults); k++ {
								toolResultContentType := contentResults[k].Get("type").String()
								if toolResultContentType == "image" {
									sourceResult := contentResults[k].Get("source")
									if sourceResult.Get("type").String() == "url" {
										if imageURL := sourceResult.Get("url").String(); imageURL != "" {
											toolResultContent := []byte(`{"type":"input_image","image_url":""}`)
											toolResultContent, _ = sjson.SetBytes(toolResultContent, "image_url", imageURL)
											toolResultContentItems = append(toolResultContentItems, toolResultContent)
										}
										continue
									}
									if sourceResult.Exists() {
										if data := claudeMediaData(sourceResult); data != "" {
											dataURL := fmt.Sprintf("data:%s;base64,%s", claudeMediaType(sourceResult), data)

											toolResultContent := []byte(`{"type":"input_image","image_url":""}`)
											toolResultContent, _ = sjson.SetBytes(toolResultContent, "image_url", dataURL)
											toolResultContentItems = append(toolResultContentItems, toolResultContent)
										}
									}
								} else if toolResultContentType == "document" {
									toolResultContentItems = appendCodexDocumentContextParts(toolResultContentItems, contentResults[k].Get("context"))
									sourceResult := contentResults[k].Get("source")
									switch sourceResult.Get("type").String() {
									case "text":
										if title := strings.TrimSpace(contentResults[k].Get("title").String()); title != "" {
											toolResultContentItems = appendCodexInputTextPart(toolResultContentItems, "Document title: "+title)
										}
										toolResultContentItems = appendCodexInputTextPart(toolResultContentItems, sourceResult.Get("data").String())
										continue
									case "content":
										if title := strings.TrimSpace(contentResults[k].Get("title").String()); title != "" {
											toolResultContentItems = appendCodexInputTextPart(toolResultContentItems, "Document title: "+title)
										}
										toolResultContentItems = appendCodexContentDocumentParts(toolResultContentItems, sourceResult.Get("content"))
										continue
									}
									fileField := "file_url"
									fileData := sourceResult.Get("url").String()
									if fileData == "" {
										fileField = "file_data"
										if data := claudeMediaData(sourceResult); data != "" {
											fileData = fmt.Sprintf("data:%s;base64,%s", claudeMediaType(sourceResult), data)
										}
									}
									if fileData != "" {
										toolResultContent := []byte(`{"type":"input_file"}`)
										toolResultContent, _ = sjson.SetBytes(toolResultContent, fileField, fileData)
										filename := strings.TrimSpace(contentResults[k].Get("title").String())
										if fileField == "file_data" {
											filename = codexInlineDocumentFilename(fileData, filename)
										}
										if filename != "" {
											toolResultContent, _ = sjson.SetBytes(toolResultContent, "filename", filename)
										}
										toolResultContentItems = append(toolResultContentItems, toolResultContent)
									}
								} else if toolResultContentType == "search_result" {
									toolResultContentItems = appendCodexInputTextPart(toolResultContentItems, claudeSearchResultText(contentResults[k]))
								} else if toolResultContentType == "text" {
									toolResultContent := []byte(`{"type":"input_text","text":""}`)
									toolResultContent, _ = sjson.SetBytes(toolResultContent, "text", contentResults[k].Get("text").String())
									toolResultContentItems = append(toolResultContentItems, toolResultContent)
								}
							}
							if len(toolResultContentItems) > 0 {
								functionCallOutputMessage, _ = sjson.SetRawBytes(functionCallOutputMessage, "output", translatorcommon.JoinRawArray(toolResultContentItems))
							} else {
								resultText := messageContentResult.Get("content").String()
								if isError {
									resultText = "Tool execution failed: " + resultText
								}
								functionCallOutputMessage, _ = sjson.SetBytes(functionCallOutputMessage, "output", resultText)
							}
						} else {
							resultText := messageContentResult.Get("content").String()
							if isError {
								resultText = "Tool execution failed: " + resultText
							}
							functionCallOutputMessage, _ = sjson.SetBytes(functionCallOutputMessage, "output", resultText)
						}

						inputItems = append(inputItems, functionCallOutputMessage)
					}
				}
				flushMessage()
			} else if messageContentsResult.Type == gjson.String {
				appendTextContent(messageContentsResult.String())
				flushMessage()
			}
		}

	}

	// Convert tools declarations to the expected format for the Codex API.
	toolsResult := rootResult.Get("tools")
	var toolItems [][]byte
	if toolsResult.IsArray() {
		webSearchToolNames := buildClaudeWebSearchToolNameSet(toolsResult)
		template, _ = sjson.SetRawBytes(template, "tool_choice", convertClaudeToolChoiceToCodex(rootResult.Get("tool_choice"), toolNameMap, webSearchToolNames))
		toolResults := toolsResult.Array()
		toolItems = make([][]byte, 0, len(toolResults))
		for i := 0; i < len(toolResults); i++ {
			toolResult := toolResults[i]
			// Special handling: map Claude web search tool to Codex web_search
			if isClaudeWebSearchToolType(toolResult.Get("type").String()) {
				toolItems = append(toolItems, convertClaudeWebSearchToolToCodex(toolResult))
				continue
			}
			tool := []byte(toolResult.Raw)
			if toolResult.Get("type").Type != gjson.String || toolResult.Get("type").String() != "function" {
				tool, _ = sjson.SetBytes(tool, "type", "function")
			}
			// Apply shortened name if needed
			if v := toolResult.Get("name"); v.Exists() {
				originalName := v.String()
				name := originalName
				if short, ok := toolNameMap[name]; ok {
					name = short
				} else {
					name = shortenNameIfNeeded(name)
				}
				if v.Type != gjson.String || name != originalName {
					tool, _ = sjson.SetBytes(tool, "name", name)
				}
			}
			parameters := normalizeToolParameters(toolResult.Get("input_schema").Raw)
			if toolResult.Get("strict").Bool() {
				parameters = normalizeCodexStrictSchema(parameters)
			} else {
				tool, _ = sjson.SetBytes(tool, "strict", false)
			}
			tool, _ = sjson.SetRawBytes(tool, "parameters", []byte(parameters))
			// allowed_callers passed validation as singleton "direct", which is
			// Codex's only invocation mode; drop the Anthropic-only field.
			for _, path := range []string{"input_schema", "parameters.$schema", "cache_control", "defer_loading", "allowed_callers"} {
				if gjson.GetBytes(tool, path).Exists() {
					tool, _ = sjson.DeleteBytes(tool, path)
				}
			}
			toolItems = append(toolItems, tool)
		}
	}

	// Default to parallel tool calls unless tool_choice explicitly disables them.
	parallelToolCalls := true
	if disableParallelToolUse := rootResult.Get("tool_choice.disable_parallel_tool_use"); disableParallelToolUse.Exists() {
		parallelToolCalls = !disableParallelToolUse.Bool()
	}

	// Add additional configuration parameters for the Codex API.
	template, _ = sjson.SetBytes(template, "parallel_tool_calls", parallelToolCalls)
	if maxTokens := rootResult.Get("max_tokens"); maxTokens.Exists() {
		template, _ = sjson.SetBytes(template, "max_output_tokens", maxTokens.Int())
	}
	if format := rootResult.Get("output_config.format"); format.Exists() {
		template, _ = sjson.SetBytes(template, "text.format.type", "json_schema")
		template, _ = sjson.SetBytes(template, "text.format.name", "anthropic_output")
		template, _ = sjson.SetBytes(template, "text.format.strict", true)
		if schema := format.Get("schema"); schema.Exists() {
			template, _ = sjson.SetRawBytes(template, "text.format.schema", []byte(normalizeCodexStrictSchema(schema.Raw)))
		}
	}

	if reasoningEffort, summaryVisible, reasoningConfigured := claudeCodexReasoning(rootResult); reasoningConfigured {
		template, _ = sjson.SetBytes(template, "reasoning.effort", reasoningEffort)
		if reasoningEffort != string(thinking.LevelNone) {
			if summaryVisible {
				template, _ = sjson.SetBytes(template, "reasoning.summary", "auto")
			}
			template, _ = sjson.SetBytes(template, "include", []string{"reasoning.encrypted_content"})
		}
	}
	serviceTier := normalizeCodexServiceTier(rootResult.Get("service_tier"))
	if speed := rootResult.Get("speed"); speed.Type == gjson.String && speed.String() == "fast" {
		serviceTier = "priority"
	}
	if serviceTier != "" {
		template, _ = sjson.SetBytes(template, "service_tier", serviceTier)
	}
	template, _ = sjson.SetBytes(template, "stream", true)
	template, _ = sjson.SetBytes(template, "store", false)
	if toolsResult.IsArray() {
		template, _ = sjson.SetRawBytes(template, "tools", translatorcommon.JoinRawArray(toolItems))
	}
	template = translatorcommon.SetRawArrayItems(template, "input", inputItems)

	return template
}

func firstNonEmptyClaudeString(result gjson.Result, paths ...string) string {
	for _, path := range paths {
		if value := strings.TrimSpace(result.Get(path).String()); value != "" {
			return value
		}
	}
	return ""
}

func claudeMediaType(source gjson.Result) string {
	if mediaType := strings.TrimSpace(source.Get("media_type").String()); mediaType != "" {
		return mediaType
	}
	if mediaType := strings.TrimSpace(source.Get("mime_type").String()); mediaType != "" {
		return mediaType
	}
	return "application/octet-stream"
}

func claudeMediaData(source gjson.Result) string {
	if data := strings.TrimSpace(source.Get("data").String()); data != "" {
		return data
	}
	return strings.TrimSpace(source.Get("base64").String())
}

func codexInlineFilename(dataURL string) string {
	return "document" + codexInlineFileExtension(dataURL)
}

// codexInlineDocumentFilename keeps the Claude document title visible while
// guaranteeing a media-type extension: Codex classifies file inputs by
// filename extension, so a bare display title like "Quarterly report" would
// reject or misclassify an otherwise valid inline PDF.
func codexInlineDocumentFilename(dataURL, title string) string {
	extension := codexInlineFileExtension(dataURL)
	title = strings.TrimSpace(title)
	if title == "" {
		return "document" + extension
	}
	if strings.HasSuffix(strings.ToLower(title), extension) {
		return title
	}
	return title + extension
}

func codexInlineFileExtension(dataURL string) string {
	mediaType := "application/octet-stream"
	if strings.HasPrefix(dataURL, "data:") {
		if separator := strings.Index(dataURL, ";"); separator > len("data:") {
			// MIME types are case-insensitive (RFC 2045); the request validator
			// accepts mixed case, so the extension lookup must too.
			mediaType = strings.ToLower(strings.TrimSpace(dataURL[len("data:"):separator]))
		}
	}
	extension := map[string]string{
		"application/pdf": ".pdf",
		"text/plain":      ".txt",
		"image/png":       ".png",
		"image/jpeg":      ".jpg",
		"image/gif":       ".gif",
		"image/webp":      ".webp",
	}[mediaType]
	if extension == "" {
		extension = ".bin"
	}
	return extension
}

func appendClaudeDocumentTitle(title string, appendText func(string)) {
	title = strings.TrimSpace(title)
	if title != "" && appendText != nil {
		appendText("Document title: " + title)
	}
}

func appendClaudeDocumentContext(contextResult gjson.Result, appendText func(string)) {
	if appendText == nil || !contextResult.Exists() {
		return
	}
	if contextResult.Type == gjson.String {
		appendText(contextResult.String())
		return
	}
	if contextResult.IsArray() {
		contextResult.ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").String() == "text" {
				appendText(block.Get("text").String())
			}
			return true
		})
	}
}

func appendClaudeContentDocumentSource(content gjson.Result, appendText, appendImage func(string)) {
	if content.Type == gjson.String {
		appendText(content.String())
		return
	}
	if !content.IsArray() {
		return
	}
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").String() {
		case "text":
			appendText(block.Get("text").String())
		case "image":
			source := block.Get("source")
			if source.Get("type").String() == "url" {
				appendImage(source.Get("url").String())
				return true
			}
			if data := claudeMediaData(source); data != "" {
				appendImage(fmt.Sprintf("data:%s;base64,%s", claudeMediaType(source), data))
			}
		}
		return true
	})
}

func appendCodexInputTextPart(parts [][]byte, text string) [][]byte {
	if text == "" {
		return parts
	}
	part := []byte(`{"type":"input_text","text":""}`)
	part, _ = sjson.SetBytes(part, "text", text)
	return append(parts, part)
}

func appendCodexDocumentContextParts(parts [][]byte, contextResult gjson.Result) [][]byte {
	if contextResult.Type == gjson.String {
		return appendCodexInputTextPart(parts, contextResult.String())
	}
	if contextResult.IsArray() {
		contextResult.ForEach(func(_, block gjson.Result) bool {
			if block.Get("type").String() == "text" {
				parts = appendCodexInputTextPart(parts, block.Get("text").String())
			}
			return true
		})
	}
	return parts
}

func appendCodexContentDocumentParts(parts [][]byte, content gjson.Result) [][]byte {
	if content.Type == gjson.String {
		return appendCodexInputTextPart(parts, content.String())
	}
	if !content.IsArray() {
		return parts
	}
	content.ForEach(func(_, block gjson.Result) bool {
		switch block.Get("type").String() {
		case "text":
			parts = appendCodexInputTextPart(parts, block.Get("text").String())
		case "image":
			source := block.Get("source")
			imageURL := source.Get("url").String()
			if imageURL == "" {
				if data := claudeMediaData(source); data != "" {
					imageURL = fmt.Sprintf("data:%s;base64,%s", claudeMediaType(source), data)
				}
			}
			if imageURL != "" {
				part := []byte(`{"type":"input_image","image_url":""}`)
				part, _ = sjson.SetBytes(part, "image_url", imageURL)
				parts = append(parts, part)
			}
		}
		return true
	})
	return parts
}

func claudeSearchResultText(block gjson.Result) string {
	var builder strings.Builder
	if title := strings.TrimSpace(block.Get("title").String()); title != "" {
		builder.WriteString("Search result: ")
		builder.WriteString(title)
		builder.WriteByte('\n')
	}
	if source := strings.TrimSpace(block.Get("source").String()); source != "" {
		builder.WriteString("Source: ")
		builder.WriteString(source)
		builder.WriteByte('\n')
	}
	content := block.Get("content")
	if content.IsArray() {
		content.ForEach(func(_, part gjson.Result) bool {
			if part.Get("type").String() == "text" {
				if text := strings.TrimSpace(part.Get("text").String()); text != "" {
					builder.WriteString(text)
					builder.WriteByte('\n')
				}
			}
			return true
		})
	}
	return strings.TrimSpace(builder.String())
}

func claudeWebSearchResultHistoryText(block gjson.Result) string {
	var builder strings.Builder
	if toolUseID := strings.TrimSpace(block.Get("tool_use_id").String()); toolUseID != "" {
		builder.WriteString("Web search results for ")
		builder.WriteString(toolUseID)
		builder.WriteString(":\n")
	}
	content := block.Get("content")
	if content.IsObject() {
		errorCode := firstNonEmptyClaudeString(content, "error_code", "type")
		if errorCode != "" {
			builder.WriteString("Web search failed: ")
			builder.WriteString(errorCode)
			builder.WriteByte('\n')
		}
	}
	if content.IsArray() {
		content.ForEach(func(_, result gjson.Result) bool {
			if result.Get("type").String() != "web_search_result" {
				return true
			}
			title := strings.TrimSpace(result.Get("title").String())
			url := strings.TrimSpace(result.Get("url").String())
			if title == "" {
				title = url
			}
			if title == "" {
				return true
			}
			builder.WriteString("- ")
			builder.WriteString(title)
			if url != "" && url != title {
				builder.WriteString(" (")
				builder.WriteString(url)
				builder.WriteString(")")
			}
			builder.WriteByte('\n')
			return true
		})
	}
	return strings.TrimSpace(builder.String())
}

func codexClaudeTargetAcceptsGrokSignature(modelName string) bool {
	baseModel := strings.ToLower(strings.TrimSpace(thinking.ParseSuffix(modelName).ModelName))
	return strings.Contains(baseModel, "grok")
}

func claudeCodexReasoning(root gjson.Result) (effort string, summaryVisible bool, enabled bool) {
	thinkingConfig := root.Get("thinking")
	thinkingType := strings.ToLower(strings.TrimSpace(thinkingConfig.Get("type").String()))
	requestedModel := util.CanonicalClaudeModelID(root.Get("model").String())
	// Effort is documented to work without extended thinking; an explicit
	// output_config.effort must survive the no-thinking default branches below.
	explicitEffort := strings.ToLower(strings.TrimSpace(root.Get("output_config.effort").String()))

	if thinkingConfig.Exists() {
		switch thinkingType {
		case "disabled":
			return string(thinking.LevelNone), false, true
		case "enabled", "adaptive", "auto":
			enabled = true
		default:
			return "", false, false
		}
	} else {
		switch {
		case requestedModel == "claude-fable-5",
			requestedModel == "claude-mythos-5",
			requestedModel == "claude-mythos-preview",
			requestedModel == "claude-sonnet-5":
			enabled = true
		case requestedModel == "claude-opus-4-8",
			requestedModel == "claude-opus-4-7",
			requestedModel == "claude-opus-4-6",
			requestedModel == "claude-sonnet-4-6":
			if explicitEffort != "" {
				return explicitEffort, false, true
			}
			return string(thinking.LevelNone), false, true
		default:
			// Preserve the previous Codex default for custom and older aliases whose
			// Claude thinking capabilities are not known at this boundary.
			if explicitEffort != "" {
				return explicitEffort, true, true
			}
			return string(thinking.LevelMedium), true, true
		}
	}

	effort = explicitEffort
	if thinkingType == "enabled" {
		if budgetTokens := thinkingConfig.Get("budget_tokens"); budgetTokens.Exists() {
			if mapped, ok := thinking.ConvertBudgetToLevel(int(budgetTokens.Int())); ok && mapped != "" {
				effort = mapped
			}
		}
	}
	if effort == "" {
		effort = string(thinking.LevelHigh)
	}
	display := strings.ToLower(strings.TrimSpace(thinkingConfig.Get("display").String()))
	summaryVisible = display == "summarized" || (thinkingType == "enabled" && display != "omitted" && !util.ClaudeThinkingDisplayOmittedByDefault(requestedModel))
	if display == "" && (thinkingType == "adaptive" || thinkingType == "auto") {
		summaryVisible = requestedModel == "claude-opus-4-6" || requestedModel == "claude-sonnet-4-6"
	}
	return effort, summaryVisible, true
}

func normalizeCodexServiceTier(result gjson.Result) string {
	if !result.Exists() || result.Type != gjson.String {
		return ""
	}

	switch strings.ToLower(strings.TrimSpace(result.String())) {
	case "fast", "priority":
		return "priority"
	default:
		return ""
	}
}

// shortenCodexCallIDIfNeeded keeps Claude tool IDs within the OpenAI Responses
// API call_id limit while preserving a stable, low-collision mapping.
func shortenCodexCallIDIfNeeded(id string) string {
	const limit = 64
	if len(id) <= limit {
		return id
	}

	sum := sha256.Sum256([]byte(id))
	suffix := "_" + hex.EncodeToString(sum[:8])
	prefixLen := limit - len(suffix)
	if prefixLen <= 0 {
		return suffix[len(suffix)-limit:]
	}
	return id[:prefixLen] + suffix
}

func isClaudeWebSearchToolType(toolType string) bool {
	return toolType == "web_search_20250305" || toolType == "web_search_20260209" || toolType == "web_search_20260318"
}

func buildClaudeWebSearchToolNameSet(tools gjson.Result) map[string]struct{} {
	names := map[string]struct{}{}
	if !tools.IsArray() {
		return names
	}

	tools.ForEach(func(_, tool gjson.Result) bool {
		toolType := tool.Get("type").String()
		if !isClaudeWebSearchToolType(toolType) {
			return true
		}

		if name := tool.Get("name").String(); name != "" {
			names[name] = struct{}{}
		}
		return true
	})

	return names
}

func convertClaudeToolChoiceToCodex(toolChoice gjson.Result, toolNameMap map[string]string, webSearchToolNames map[string]struct{}) []byte {
	if !toolChoice.Exists() || toolChoice.Type == gjson.Null {
		return []byte(`"auto"`)
	}

	choiceType := toolChoice.Get("type").String()
	if choiceType == "" && toolChoice.Type == gjson.String {
		choiceType = toolChoice.String()
	}

	switch choiceType {
	case "auto", "":
		return []byte(`"auto"`)
	case "any":
		return []byte(`"required"`)
	case "none":
		return []byte(`"none"`)
	case "tool":
		name := toolChoice.Get("name").String()
		if _, ok := webSearchToolNames[name]; ok {
			return []byte(`{"type":"web_search"}`)
		}
		if short, ok := toolNameMap[name]; ok {
			name = short
		} else {
			name = shortenNameIfNeeded(name)
		}
		if name == "" {
			return []byte(`"auto"`)
		}

		choice := []byte(`{"type":"function","name":""}`)
		choice, _ = sjson.SetBytes(choice, "name", name)
		return choice
	default:
		return []byte(`"auto"`)
	}
}

func convertClaudeWebSearchToolToCodex(tool gjson.Result) []byte {
	out := []byte(`{"type":"web_search"}`)
	if allowedDomains := tool.Get("allowed_domains"); allowedDomains.Exists() && allowedDomains.IsArray() {
		out, _ = sjson.SetRawBytes(out, "filters.allowed_domains", []byte(allowedDomains.Raw))
	}
	if userLocation := tool.Get("user_location"); userLocation.Exists() && userLocation.IsObject() {
		out, _ = sjson.SetRawBytes(out, "user_location", []byte(userLocation.Raw))
	}
	return out
}

// shortenNameIfNeeded applies a simple shortening rule for a single name.
func shortenNameIfNeeded(name string) string {
	const limit = 64
	if len(name) <= limit {
		return name
	}
	if strings.HasPrefix(name, "mcp__") {
		idx := strings.LastIndex(name, "__")
		if idx > 0 {
			cand := "mcp__" + name[idx+2:]
			if len(cand) > limit {
				return cand[:limit]
			}
			return cand
		}
	}
	return name[:limit]
}

// buildShortNameMap ensures uniqueness of shortened names within a request.
func buildShortNameMap(names []string) map[string]string {
	const limit = 64
	used := map[string]struct{}{}
	m := map[string]string{}

	baseCandidate := func(n string) string {
		if len(n) <= limit {
			return n
		}
		if strings.HasPrefix(n, "mcp__") {
			idx := strings.LastIndex(n, "__")
			if idx > 0 {
				cand := "mcp__" + n[idx+2:]
				if len(cand) > limit {
					cand = cand[:limit]
				}
				return cand
			}
		}
		return n[:limit]
	}

	makeUnique := func(cand string) string {
		if _, ok := used[cand]; !ok {
			return cand
		}
		base := cand
		for i := 1; ; i++ {
			suffix := "_" + strconv.Itoa(i)
			allowed := limit - len(suffix)
			if allowed < 0 {
				allowed = 0
			}
			tmp := base
			if len(tmp) > allowed {
				tmp = tmp[:allowed]
			}
			tmp = tmp + suffix
			if _, ok := used[tmp]; !ok {
				return tmp
			}
		}
	}

	for _, n := range names {
		cand := baseCandidate(n)
		uniq := makeUnique(cand)
		used[uniq] = struct{}{}
		m[n] = uniq
	}
	return m
}

// buildReverseMapFromClaudeOriginalToShort builds original->short map, used to map tool_use names to short.
func buildReverseMapFromClaudeOriginalToShort(original []byte) map[string]string {
	tools := gjson.GetBytes(original, "tools")
	m := map[string]string{}
	if !tools.IsArray() {
		return m
	}
	var names []string
	arr := tools.Array()
	for i := 0; i < len(arr); i++ {
		n := arr[i].Get("name").String()
		if n != "" {
			names = append(names, n)
		}
	}
	if len(names) > 0 {
		m = buildShortNameMap(names)
	}
	return m
}

func normalizeCodexStrictSchema(raw string) string {
	normalized, err := translatorcommon.NormalizeCodexStrictSchema(raw)
	if err != nil {
		return raw
	}
	return normalized
}

// normalizeToolParameters ensures object schemas contain at least an empty properties map.
func normalizeToolParameters(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || !gjson.Valid(raw) {
		return `{"type":"object","properties":{}}`
	}
	result := gjson.Parse(raw)
	schema := []byte(raw)
	schemaType := result.Get("type").String()
	if schemaType == "" {
		schema, _ = sjson.SetBytes(schema, "type", "object")
		schemaType = "object"
	}
	if schemaType == "object" && !result.Get("properties").Exists() {
		schema, _ = sjson.SetRawBytes(schema, "properties", []byte(`{}`))
	}
	return string(schema)
}
