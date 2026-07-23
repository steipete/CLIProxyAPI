package claude

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	internallogging "github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

const claudeRequestIDHeader = "request-id"

func ensureClaudeRequestID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	requestID := strings.TrimSpace(internallogging.GetGinRequestID(c))
	if requestID == "" {
		requestID = internallogging.GenerateRequestID()
		internallogging.SetGinRequestID(c, requestID)
	}
	if !strings.HasPrefix(requestID, "req_") {
		requestID = "req_" + requestID
	}
	return setClaudeRequestID(c, requestID)
}

func setClaudeRequestID(c *gin.Context, requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if c == nil || requestID == "" {
		return requestID
	}
	c.Header(claudeRequestIDHeader, requestID)
	return requestID
}

// WriteAuthenticationError writes an Anthropic-compatible authentication error.
func WriteAuthenticationError(c *gin.Context, status int, message string) {
	if status <= 0 {
		status = http.StatusUnauthorized
	}
	response := claudeErrorResponse{
		Type:      "error",
		RequestID: ensureClaudeRequestID(c),
		Error: claudeErrorDetail{
			Type:    claudeErrorTypeFromStatus(status),
			Message: strings.TrimSpace(message),
		},
	}
	if response.Error.Message == "" {
		response.Error.Message = http.StatusText(status)
	}
	c.AbortWithStatusJSON(status, response)
}
