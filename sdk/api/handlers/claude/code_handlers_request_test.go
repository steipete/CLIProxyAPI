package claude

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

func TestClaudeMessagesRejectsMalformedRequestBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{bad"))
	handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})

	handler.ClaudeMessages(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	body := recorder.Body.Bytes()
	if got := gjson.GetBytes(body, "error.type").String(); got != "invalid_request_error" {
		t.Fatalf("error.type = %q; body=%s", got, body)
	}
	requestID := gjson.GetBytes(body, "request_id").String()
	if requestID == "" || recorder.Header().Get(claudeRequestIDHeader) != requestID {
		t.Fatalf("request ID body=%q header=%q", requestID, recorder.Header().Get(claudeRequestIDHeader))
	}
}

func TestClaudeCountTokensRejectsMalformedRequestBeforeExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-fable-5"}`))
	handler := NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{})

	handler.ClaudeCountTokens(c)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
