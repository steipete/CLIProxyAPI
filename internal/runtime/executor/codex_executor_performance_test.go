package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func BenchmarkCodexExecuteStreamDeferredRequestCapture(b *testing.B) {
	gin.SetMode(gin.TestMode)
	for _, size := range []int{64 << 10, 1 << 20, 8 << 20} {
		b.Run(byteSizeName(size), func(b *testing.B) {
			payload := benchmarkClaudeRequest(b, size)
			stream := benchmarkCodexStream(1)
			executor := NewCodexExecutor(&config.Config{})
			auth := benchmarkCodexAuth()
			request := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: payload}
			options := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, Stream: true}

			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for b.Loop() {
				ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
				ctx := benchmarkCodexContext(stream)
				ctx = context.WithValue(ctx, "gin", ginCtx)
				benchmarkDrainCodexStream(b, executor, ctx, auth, request, options)
			}
		})
	}
}

func BenchmarkCodexExecuteStreamClaudeSSE(b *testing.B) {
	payload := benchmarkClaudeRequest(b, 256)
	executor := NewCodexExecutor(&config.Config{})
	auth := benchmarkCodexAuth()
	request := cliproxyexecutor.Request{Model: "gpt-5.6-sol", Payload: payload}
	options := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude, Stream: true}

	for _, eventCount := range []int{1, 100, 1000} {
		b.Run("Events"+strconv.Itoa(eventCount), func(b *testing.B) {
			stream := benchmarkCodexStream(eventCount)
			b.ReportAllocs()
			b.SetBytes(int64(len(stream)))
			b.ResetTimer()
			for b.Loop() {
				benchmarkDrainCodexStream(b, executor, benchmarkCodexContext(stream), auth, request, options)
			}
		})
	}
}

func benchmarkClaudeRequest(b *testing.B, contentSize int) []byte {
	b.Helper()
	payload, err := json.Marshal(map[string]any{
		"model":      "claude-fable-5",
		"stream":     true,
		"max_tokens": 64,
		"messages": []map[string]any{{
			"role":    "user",
			"content": strings.Repeat("a", contentSize),
		}},
	})
	if err != nil {
		b.Fatalf("marshal benchmark request: %v", err)
	}
	return payload
}

func benchmarkCodexStream(eventCount int) []byte {
	var stream bytes.Buffer
	stream.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bench\",\"model\":\"gpt-5.6-sol\"}}\n")
	for range eventCount {
		stream.WriteString("data: {\"type\":\"response.output_text.delta\",\"delta\":\"benchmark token\"}\n")
	}
	stream.WriteString("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bench\",\"model\":\"gpt-5.6-sol\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":8,\"output_tokens\":16,\"total_tokens\":24}}}\n")
	return stream.Bytes()
}

func benchmarkCodexAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID: "benchmark-auth",
		Attributes: map[string]string{
			"base_url": "http://codex.test",
			"api_key":  "test",
		},
	}
}

func benchmarkCodexContext(stream []byte) context.Context {
	return context.WithValue(context.Background(), "cliproxy.roundtripper", benchmarkRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if _, err := io.Copy(io.Discard, req.Body); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewReader(stream)),
			Request:    req,
		}, nil
	}))
}

func benchmarkDrainCodexStream(
	b *testing.B,
	executor *CodexExecutor,
	ctx context.Context,
	auth *cliproxyauth.Auth,
	request cliproxyexecutor.Request,
	options cliproxyexecutor.Options,
) {
	b.Helper()
	result, err := executor.ExecuteStream(ctx, auth, request, options)
	if err != nil {
		b.Fatalf("ExecuteStream: %v", err)
	}
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			b.Fatalf("stream chunk: %v", chunk.Err)
		}
	}
}

type benchmarkRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f benchmarkRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func byteSizeName(size int) string {
	switch {
	case size%(1<<20) == 0:
		return strconv.Itoa(size/(1<<20)) + "MiB"
	case size%(1<<10) == 0:
		return strconv.Itoa(size/(1<<10)) + "KiB"
	default:
		return strconv.Itoa(size) + "B"
	}
}
