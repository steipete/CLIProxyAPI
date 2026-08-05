package helps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

func TestRecordAPIRequestClonesDeferredBodyWhenRequestLogDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	body := []byte(`{"model":"original"}`)

	RecordAPIRequest(ctx, &config.Config{}, UpstreamRequestLog{
		URL:    "https://api.example.com/v1/responses",
		Method: http.MethodPost,
		Body:   body,
	})
	body[10] = 'X'

	value, exists := ginCtx.Get(logging.DeferredAPIRequestContextKey)
	if !exists {
		t.Fatal("deferred API request was not captured")
	}
	requests, ok := value.([]logging.DeferredAPIRequest)
	if !ok || len(requests) != 1 {
		t.Fatalf("deferred API requests = %#v, want one request", value)
	}
	captured := string(requests[0]())
	if !strings.Contains(captured, `{"model":"original"}`) {
		t.Fatalf("captured API request = %q, want original body", captured)
	}
}

func TestRecordAPIRequestWithImmutableBodyRetainsBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	body := []byte(`{"model":"original"}`)

	RecordAPIRequestWithImmutableBody(ctx, &config.Config{}, UpstreamRequestLog{
		URL:    "https://api.example.com/v1/responses",
		Method: http.MethodPost,
		Body:   body,
	})
	// Deliberately violate the caller contract to prove this path retains rather than clones Body.
	body[10] = 'X'

	captured := deferredAPIRequestAt(t, ginCtx, 0)
	if !strings.Contains(captured, `{"model":"Xriginal"}`) {
		t.Fatalf("captured API request = %q, want retained immutable body", captured)
	}
}

func TestRecordAPIRequestWithImmutableBodyPreservesEmptyAndTruncatedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("empty", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		ctx := context.WithValue(context.Background(), "gin", ginCtx)

		RecordAPIRequestWithImmutableBody(ctx, &config.Config{}, UpstreamRequestLog{})

		if captured := deferredAPIRequestAt(t, ginCtx, 0); !strings.Contains(captured, "Body:\n<empty>") {
			t.Fatalf("captured API request = %q, want empty body marker", captured)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		ginCtx.Set(deferredAPIRequestBytesKey, maxDeferredAPIRequestBodyBytes-4)
		ctx := context.WithValue(context.Background(), "gin", ginCtx)

		RecordAPIRequestWithImmutableBody(ctx, &config.Config{}, UpstreamRequestLog{Body: []byte("12345678")})

		captured := deferredAPIRequestAt(t, ginCtx, 0)
		if !strings.Contains(captured, "Body:\n1234\n[API REQUEST BODY TRUNCATED: captured first 4 bytes]") {
			t.Fatalf("captured API request = %q, want four-byte truncation", captured)
		}
		if got, _ := ginCtx.Get(deferredAPIRequestBytesKey); got != maxDeferredAPIRequestBodyBytes {
			t.Fatalf("captured bytes = %v, want %d", got, maxDeferredAPIRequestBodyBytes)
		}
	})
}

func deferredAPIRequestAt(t *testing.T, ginCtx *gin.Context, index int) string {
	t.Helper()
	value, exists := ginCtx.Get(logging.DeferredAPIRequestContextKey)
	if !exists {
		t.Fatal("deferred API request was not captured")
	}
	requests, ok := value.([]logging.DeferredAPIRequest)
	if !ok || len(requests) <= index {
		t.Fatalf("deferred API requests = %#v, want index %d", value, index)
	}
	return string(requests[index]())
}

func TestRecordAPIResponseMetadataStoresHeadersWhenRequestLogDisabled(t *testing.T) {
	ctx := logging.WithResponseHeadersHolder(context.Background())
	headers := http.Header{}
	headers.Add("X-Upstream-Request-Id", "upstream-req-1")

	RecordAPIResponseMetadata(ctx, &config.Config{}, http.StatusOK, headers)
	headers.Set("X-Upstream-Request-Id", "mutated")

	got := logging.GetResponseHeaders(ctx)
	if got.Get("X-Upstream-Request-Id") != "upstream-req-1" {
		t.Fatalf("response header = %q, want %q", got.Get("X-Upstream-Request-Id"), "upstream-req-1")
	}
}
