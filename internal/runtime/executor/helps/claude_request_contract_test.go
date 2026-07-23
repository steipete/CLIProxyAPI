package helps

import "testing"

func TestValidateClaudeRequestForCodex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "supported text", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hello"}]}`},
		{name: "supported url image", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}]}`},
		{name: "supported base64 document", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}]}]}`},
		{name: "file image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"file","file_id":"file_1"}}]}]}`, wantErr: true},
		{name: "empty url image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":""}}]}]}`, wantErr: true},
		{name: "relative url image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"/a.png"}}]}]}`, wantErr: true},
		{name: "empty base64 image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":""}}]}]}`, wantErr: true},
		{name: "missing base64 media type rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"AA=="}}]}]}`, wantErr: true},
		{name: "invalid base64 image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"not base64"}}]}]}`, wantErr: true},
		{name: "base64 image aliases supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","mime_type":"image/png","base64":"AA=="}}]}]}`},
		{name: "non-image media type rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"application/pdf","data":"AA=="}}]}]}`, wantErr: true},
		{name: "non-pdf document media type rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"base64","media_type":"image/png","data":"AA=="}}]}]}`, wantErr: true},
		{name: "basic web search default supported", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search"}]}`},
		{name: "current direct web search supported", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","allowed_callers":["direct"]}],"messages":[{"role":"user","content":"search"}]}`},
		{name: "custom tool direct caller supported", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","description":"d","input_schema":{"type":"object","properties":{},"required":[],"additionalProperties":false},"allowed_callers":["direct"]}],"messages":[{"role":"user","content":"hi"}]}`},
		{name: "custom tool code execution caller rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","description":"d","input_schema":{"type":"object","properties":{},"required":[],"additionalProperties":false},"allowed_callers":["code_execution_20260120"]}],"messages":[{"role":"user","content":"hi"}]}`, wantErr: true},
		{name: "custom tool mixed callers rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","description":"d","input_schema":{"type":"object","properties":{},"required":[],"additionalProperties":false},"allowed_callers":["direct","code_execution_20260120"]}],"messages":[{"role":"user","content":"hi"}]}`, wantErr: true},
		{name: "current web search default rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search"}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "dynamic filtering web search default rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260209","name":"web_search"}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "full response inclusion supported", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","allowed_callers":["direct"],"response_inclusion":"full"}],"messages":[{"role":"user","content":"search"}]}`},
		{name: "excluded response inclusion rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","response_inclusion":"excluded"}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "web search max uses rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","max_uses":1}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "web search code execution caller rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","allowed_callers":["code_execution_20260120"]}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "web search mixed callers rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260318","name":"web_search","allowed_callers":["direct","code_execution_20260120"]}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "blocked domains rejected", payload: `{"model":"claude-opus-4-8","tools":[{"type":"web_search_20260209","name":"web_search","blocked_domains":["blocked.example"]}],"messages":[{"role":"user","content":"search"}]}`, wantErr: true},
		{name: "text document supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"text","media_type":"text/plain","data":"hello"}}]}]}`},
		{name: "content document supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"content","content":"hello"}}]}]}`},
		{name: "content document url image supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"content","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}}]}]}`},
		{name: "content document base64 image aliases supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"content","content":[{"type":"image","source":{"type":"base64","mime_type":"image/png","base64":"AA=="}}]}}]}]}`},
		{name: "content document file image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","source":{"type":"content","content":[{"type":"image","source":{"type":"file","file_id":"file_1"}}]}}]}]}`, wantErr: true},
		{name: "tool result content document file image rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"document","source":{"type":"content","content":[{"type":"image","source":{"type":"file","file_id":"file_1"}}]}}]}]}]}`, wantErr: true},
		{name: "document citations rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"document","citations":{"enabled":true},"source":{"type":"text","media_type":"text/plain","data":"hello"}}]}]}`, wantErr: true},
		{name: "encrypted web search replay rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"assistant","content":[{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","title":"Result","url":"https://example.com","encrypted_content":"opaque"}]}]}]}`, wantErr: true},
		{name: "plain web search replay supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"assistant","content":[{"type":"web_search_tool_result","tool_use_id":"srv_1","content":[{"type":"web_search_result","title":"Result","url":"https://example.com"}]}]}]}`},
		{name: "search result supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"search_result","source":"https://example.com","title":"Example","content":[{"type":"text","text":"evidence"}]}]}]}`},
		{name: "search result citations rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"search_result","source":"https://example.com","title":"Example","content":[{"type":"text","text":"evidence"}],"citations":{"enabled":true}}]}]}`, wantErr: true},
		{name: "empty search result content rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"search_result","source":"https://example.com","title":"Example","content":[]}]}]}`, wantErr: true},
		{name: "empty search result text rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"search_result","source":"https://example.com","title":"Example","content":[{"type":"text","text":"  "}]}]}]}`, wantErr: true},
		{name: "tool result search result supported", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"search_result","source":"kb://item-1","title":"Item","content":[{"type":"text","text":"evidence"}]}]}]}]}`},
		{name: "tool result search citations rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"search_result","source":"kb://item-1","title":"Item","content":[{"type":"text","text":"evidence"}],"citations":{"enabled":true}}]}]}]}`, wantErr: true},
		{name: "unknown block rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"future_block"}]}]}`, wantErr: true},
		{name: "structured output supported", payload: `{"model":"claude-opus-4-8","output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{},"required":[],"additionalProperties":false}}},"messages":[{"role":"user","content":"hello"}]}`},
		{name: "optional structured output rejected", payload: `{"model":"claude-opus-4-8","output_config":{"format":{"type":"json_schema","schema":{"type":"object","properties":{"value":{"type":"string"}}}}},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "strict tool supported", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}}],"messages":[{"role":"user","content":"hello"}]}`},
		{name: "explicit custom tool supported", payload: `{"model":"claude-opus-4-8","tools":[{"type":"custom","name":"lookup","input_schema":{"type":"object","properties":{"value":{"type":"string"}}}}],"messages":[{"role":"user","content":"hello"}]}`},
		{name: "deferred tool rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","defer_loading":true,"input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "programmatic tool caller rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","allowed_callers":["code_execution_20260120"],"input_schema":{"type":"object","properties":{}}}],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "optional strict tool rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","strict":true,"input_schema":{"type":"object","properties":{"value":{"type":"string"}}}}],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "string root strict tool rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","strict":true,"input_schema":{"type":"string","minLength":1}}],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "nullable object root strict tool rejected", payload: `{"model":"claude-opus-4-8","tools":[{"name":"lookup","strict":true,"input_schema":{"type":["object","null"],"properties":{},"required":[],"additionalProperties":false}}],"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "invalid output format rejected", payload: `{"model":"claude-opus-4-8","output_config":{"format":{"type":"text"}},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "non-object structured output rejected", payload: `{"model":"claude-opus-4-8","output_config":{"format":{"type":"json_schema","schema":{"type":"string"}}},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "nullable structured output root rejected", payload: `{"model":"claude-opus-4-8","output_config":{"format":{"type":"json_schema","schema":{"type":["object","null"],"properties":{},"required":[],"additionalProperties":false}}},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "unsupported server tool history rejected", payload: `{"model":"claude-opus-4-8","messages":[{"role":"assistant","content":[{"type":"server_tool_use","id":"srv_1","name":"web_fetch","input":{"url":"https://example.com"}}]}]}`, wantErr: true},
		{name: "fable disabled rejected", payload: `{"model":"claude-fable-5","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "fable context alias disabled rejected", payload: `{"model":"claude-fable-5[1m]","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "fable prefix custom alias preserves legacy behavior", payload: `{"model":"claude-fable-5-internal","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`},
		{name: "opus 4.8 manual thinking rejected", payload: `{"model":"claude-opus-4-8","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "sonnet 5 manual thinking rejected", payload: `{"model":"claude-sonnet-5","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
		{name: "mythos preview manual thinking supported", payload: `{"model":"claude-mythos-preview","thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hello"}]}`},
		{name: "mythos preview disabled rejected", payload: `{"model":"claude-mythos-preview","thinking":{"type":"disabled"},"messages":[{"role":"user","content":"hello"}]}`, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateClaudeRequestForCodex([]byte(test.payload))
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
