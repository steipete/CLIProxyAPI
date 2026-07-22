package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type failOnceStreamExecutor struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (e *failOnceStreamExecutor) Identifier() string { return "codex" }

func (e *failOnceStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *failOnceStreamExecutor) ExecuteStream(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	delay := e.delay
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 1)
	if call == 1 {
		ch <- coreexecutor.StreamChunk{
			Err: &coreauth.Error{
				Code:       "unauthorized",
				Message:    "unauthorized",
				Retryable:  false,
				HTTPStatus: http.StatusUnauthorized,
			},
		}
		close(ch)
		return &coreexecutor.StreamResult{
			Headers: http.Header{"X-Upstream-Attempt": {"1"}},
			Chunks:  ch,
		}, nil
	}

	if delay > 0 {
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				close(ch)
			case <-timer.C:
				ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
				close(ch)
			}
		}()
	} else {
		ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
		close(ch)
	}
	return &coreexecutor.StreamResult{
		Headers:          http.Header{"X-Upstream-Attempt": {"2"}},
		Chunks:           ch,
		UpstreamAccepted: true,
	}, nil
}

func (e *failOnceStreamExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *failOnceStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *failOnceStreamExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{
		Code:       "not_implemented",
		Message:    "HttpRequest not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}

func (e *failOnceStreamExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type acceptedErrorStreamExecutor struct {
	mu    sync.Mutex
	calls int
	delay time.Duration
}

func (e *acceptedErrorStreamExecutor) Identifier() string { return "codex" }

func (e *acceptedErrorStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *acceptedErrorStreamExecutor) ExecuteStream(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls++
	delay := e.delay
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 1)
	sendError := func() {
		ch <- coreexecutor.StreamChunk{Err: &coreauth.Error{
			Code:       "upstream_closed",
			Message:    "upstream closed after acceptance",
			HTTPStatus: http.StatusBadGateway,
		}}
		close(ch)
	}
	if delay > 0 {
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				close(ch)
			case <-timer.C:
				sendError()
			}
		}()
	} else {
		sendError()
	}
	return &coreexecutor.StreamResult{
		Headers:          http.Header{"X-Upstream-Accepted": {"true"}},
		Chunks:           ch,
		UpstreamAccepted: true,
	}, nil
}

func (e *acceptedErrorStreamExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *acceptedErrorStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *acceptedErrorStreamExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented", HTTPStatus: http.StatusNotImplemented}
}

func (e *acceptedErrorStreamExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type cancelAwareRetryExecutor struct {
	mu            sync.Mutex
	calls         int
	firstCanceled chan struct{}
}

func (e *cancelAwareRetryExecutor) Identifier() string { return "codex" }

func (e *cancelAwareRetryExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *cancelAwareRetryExecutor) ExecuteStream(ctx context.Context, _ *coreauth.Auth, _ coreexecutor.Request, _ coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 1)
	if call == 1 {
		ch <- coreexecutor.StreamChunk{Err: &coreauth.Error{
			Code:       "upstream_closed",
			Message:    "upstream closed",
			HTTPStatus: http.StatusBadGateway,
		}}
		go func() {
			<-ctx.Done()
			close(e.firstCanceled)
			close(ch)
		}()
		return &coreexecutor.StreamResult{Chunks: ch}, nil
	}

	ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *cancelAwareRetryExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *cancelAwareRetryExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *cancelAwareRetryExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented", HTTPStatus: http.StatusNotImplemented}
}

func (e *cancelAwareRetryExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type dropThenErrorRetryExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *dropThenErrorRetryExecutor) Identifier() string { return "codex" }

func (e *dropThenErrorRetryExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *dropThenErrorRetryExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 2)
	if call == 1 {
		ch <- coreexecutor.StreamChunk{Payload: []byte("drop")}
		ch <- coreexecutor.StreamChunk{Err: &coreauth.Error{
			Code:       "upstream_closed",
			Message:    "upstream closed before forwarded payload",
			HTTPStatus: http.StatusBadGateway,
		}}
	} else {
		ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
	}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *dropThenErrorRetryExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *dropThenErrorRetryExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *dropThenErrorRetryExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{Code: "not_implemented", Message: "HttpRequest not implemented", HTTPStatus: http.StatusNotImplemented}
}

func (e *dropThenErrorRetryExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type payloadThenErrorStreamExecutor struct {
	mu    sync.Mutex
	calls int
}

func (e *payloadThenErrorStreamExecutor) Identifier() string { return "codex" }

func (e *payloadThenErrorStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *payloadThenErrorStreamExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()

	ch := make(chan coreexecutor.StreamChunk, 2)
	ch <- coreexecutor.StreamChunk{Payload: []byte("partial")}
	ch <- coreexecutor.StreamChunk{
		Err: &coreauth.Error{
			Code:       "upstream_closed",
			Message:    "upstream closed",
			Retryable:  false,
			HTTPStatus: http.StatusBadGateway,
		},
	}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *payloadThenErrorStreamExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *payloadThenErrorStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *payloadThenErrorStreamExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{
		Code:       "not_implemented",
		Message:    "HttpRequest not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}

func (e *payloadThenErrorStreamExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type authAwareStreamExecutor struct {
	mu      sync.Mutex
	calls   int
	authIDs []string
}

type invalidJSONStreamExecutor struct{}

type splitResponsesEventStreamExecutor struct{}

func (e *invalidJSONStreamExecutor) Identifier() string { return "codex" }

func (e *invalidJSONStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *invalidJSONStreamExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	ch := make(chan coreexecutor.StreamChunk, 1)
	ch <- coreexecutor.StreamChunk{Payload: []byte("event: response.completed\ndata: {\"type\"")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *invalidJSONStreamExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *invalidJSONStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *invalidJSONStreamExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{
		Code:       "not_implemented",
		Message:    "HttpRequest not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}

func (e *splitResponsesEventStreamExecutor) Identifier() string { return "split-sse" }

func (e *splitResponsesEventStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *splitResponsesEventStreamExecutor) ExecuteStream(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	ch := make(chan coreexecutor.StreamChunk, 2)
	ch <- coreexecutor.StreamChunk{Payload: []byte("event: response.completed")}
	ch <- coreexecutor.StreamChunk{Payload: []byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"output\":[]}}")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *splitResponsesEventStreamExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *splitResponsesEventStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *splitResponsesEventStreamExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{
		Code:       "not_implemented",
		Message:    "HttpRequest not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}

func (e *authAwareStreamExecutor) Identifier() string { return "codex" }

func (e *authAwareStreamExecutor) Execute(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *authAwareStreamExecutor) ExecuteStream(ctx context.Context, auth *coreauth.Auth, req coreexecutor.Request, opts coreexecutor.Options) (*coreexecutor.StreamResult, error) {
	_ = ctx
	_ = req
	_ = opts
	ch := make(chan coreexecutor.StreamChunk, 1)

	authID := ""
	if auth != nil {
		authID = auth.ID
	}

	e.mu.Lock()
	e.calls++
	e.authIDs = append(e.authIDs, authID)
	e.mu.Unlock()

	if authID == "auth1" {
		ch <- coreexecutor.StreamChunk{
			Err: &coreauth.Error{
				Code:       "unauthorized",
				Message:    "unauthorized",
				Retryable:  false,
				HTTPStatus: http.StatusUnauthorized,
			},
		}
		close(ch)
		return &coreexecutor.StreamResult{Chunks: ch}, nil
	}

	ch <- coreexecutor.StreamChunk{Payload: []byte("ok")}
	close(ch)
	return &coreexecutor.StreamResult{Chunks: ch}, nil
}

func (e *authAwareStreamExecutor) Refresh(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (e *authAwareStreamExecutor) CountTokens(context.Context, *coreauth.Auth, coreexecutor.Request, coreexecutor.Options) (coreexecutor.Response, error) {
	return coreexecutor.Response{}, &coreauth.Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *authAwareStreamExecutor) HttpRequest(ctx context.Context, auth *coreauth.Auth, req *http.Request) (*http.Response, error) {
	return nil, &coreauth.Error{
		Code:       "not_implemented",
		Message:    "HttpRequest not implemented",
		HTTPStatus: http.StatusNotImplemented,
	}
}

func (e *authAwareStreamExecutor) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *authAwareStreamExecutor) AuthIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.authIDs))
	copy(out, e.authIDs)
	return out
}

func TestExecuteStreamWithAuthManager_RetriesBeforeFirstByte(t *testing.T) {
	executor := &failOnceStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	auth2 := &coreauth.Auth{
		ID:       "auth2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test2@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth2); err != nil {
		t.Fatalf("manager.Register(auth2): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	registry.GetGlobalRegistry().RegisterClient(auth2.ID, auth2.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
		registry.GetGlobalRegistry().UnregisterClient(auth2.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		PassthroughHeaders: true,
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 1,
		},
	}, manager)
	dataChan, upstreamHeaders, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}

	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}

	if string(got) != "ok" {
		t.Fatalf("expected payload ok, got %q", string(got))
	}
	if executor.Calls() != 2 {
		t.Fatalf("expected 2 stream attempts, got %d", executor.Calls())
	}
	upstreamAttemptHeader := upstreamHeaders.Get("X-Upstream-Attempt")
	if upstreamAttemptHeader != "2" {
		t.Fatalf("expected upstream header from retry attempt, got %q", upstreamAttemptHeader)
	}
}

func TestExecuteStreamWithAuthManager_DoesNotRetryAfterUpstreamAcceptance(t *testing.T) {
	executor := &acceptedErrorStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	for _, authID := range []string{"auth1", "auth2"} {
		auth := &coreauth.Auth{
			ID:       authID,
			Provider: "codex",
			Status:   coreauth.StatusActive,
			Metadata: map[string]any{"email": authID + "@example.com"},
		}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("manager.Register(%s): %v", authID, err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		PassthroughHeaders: true,
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 3,
		},
	}, manager)
	dataChan, upstreamHeaders, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")

	for chunk := range dataChan {
		t.Fatalf("unexpected payload after accepted stream failure: %q", chunk)
	}
	var gotErr *interfaces.ErrorMessage
	for msg := range errChan {
		if msg != nil {
			gotErr = msg
		}
	}
	if gotErr == nil || gotErr.Error == nil {
		t.Fatal("expected accepted stream error")
	}
	if gotErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", gotErr.StatusCode, http.StatusBadGateway)
	}
	if executor.Calls() != 1 {
		t.Fatalf("stream attempts = %d, want 1 after upstream acceptance", executor.Calls())
	}
	if got := upstreamHeaders.Get("X-Upstream-Accepted"); got != "true" {
		t.Fatalf("X-Upstream-Accepted = %q, want true", got)
	}
}

func TestExecuteStreamWithAuthManager_ReturnsAcceptedStreamBeforeFirstEvent(t *testing.T) {
	executor := &acceptedErrorStreamExecutor{delay: 150 * time.Millisecond}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	auth := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "auth1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("manager.Register: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	started := time.Now()
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Fatalf("accepted stream returned after %v, want before first event", elapsed)
	}
	for range dataChan {
		t.Fatal("unexpected payload")
	}
	var gotErr *interfaces.ErrorMessage
	for msg := range errChan {
		if msg != nil {
			gotErr = msg
		}
	}
	if gotErr == nil || gotErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("accepted stream error = %#v, want status %d", gotErr, http.StatusBadGateway)
	}
	if executor.Calls() != 1 {
		t.Fatalf("stream attempts = %d, want 1", executor.Calls())
	}
}

func TestExecuteStreamWithAuthManager_CancelsFailedAttemptBeforeRetry(t *testing.T) {
	executor := &cancelAwareRetryExecutor{firstCanceled: make(chan struct{})}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	for _, authID := range []string{"auth1", "auth2"} {
		auth := &coreauth.Auth{
			ID:       authID,
			Provider: "codex",
			Status:   coreauth.StatusActive,
			Metadata: map[string]any{"email": authID + "@example.com"},
		}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("manager.Register(%s): %v", authID, err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	var payload []byte
	for chunk := range dataChan {
		payload = append(payload, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}
	if string(payload) != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	select {
	case <-executor.firstCanceled:
	case <-time.After(time.Second):
		t.Fatal("failed stream attempt was not canceled before retry")
	}
	if executor.Calls() != 2 {
		t.Fatalf("stream attempts = %d, want 2", executor.Calls())
	}
}

func TestExecuteStreamWithAuthManager_RetriesAfterInterceptorDropsInitialChunk(t *testing.T) {
	executor := &dropThenErrorRetryExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	for _, authID := range []string{"auth1", "auth2"} {
		auth := &coreauth.Auth{
			ID:       authID,
			Provider: "codex",
			Status:   coreauth.StatusActive,
			Metadata: map[string]any{"email": authID + "@example.com"},
		}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("manager.Register(%s): %v", authID, err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	}

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{BootstrapRetries: 1},
	}, manager)
	handler.SetPluginHost(&handlerInterceptorTestHost{
		interceptStreamChunk: func(_ context.Context, req pluginapi.StreamChunkInterceptRequest) pluginapi.StreamChunkInterceptResponse {
			if string(req.Body) == "drop" {
				return pluginapi.StreamChunkInterceptResponse{DropChunk: true}
			}
			return pluginapi.StreamChunkInterceptResponse{Body: cloneBytes(req.Body)}
		},
	})

	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	var payload []byte
	for chunk := range dataChan {
		payload = append(payload, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}
	if string(payload) != "ok" {
		t.Fatalf("payload = %q, want ok", payload)
	}
	if executor.Calls() != 2 {
		t.Fatalf("stream attempts = %d, want 2", executor.Calls())
	}
}

func TestExecuteStreamWithAuthManager_HeaderPassthroughDisabledByDefault(t *testing.T) {
	executor := &failOnceStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	auth2 := &coreauth.Auth{
		ID:       "auth2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test2@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth2); err != nil {
		t.Fatalf("manager.Register(auth2): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	registry.GetGlobalRegistry().RegisterClient(auth2.ID, auth2.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
		registry.GetGlobalRegistry().UnregisterClient(auth2.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 1,
		},
	}, manager)
	dataChan, upstreamHeaders, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}

	if string(got) != "ok" {
		t.Fatalf("expected payload ok, got %q", string(got))
	}
	if upstreamHeaders != nil {
		t.Fatalf("expected nil upstream headers when passthrough is disabled, got %#v", upstreamHeaders)
	}
}

func TestExecuteStreamWithAuthManager_DoesNotRetryAfterFirstByte(t *testing.T) {
	executor := &payloadThenErrorStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	auth2 := &coreauth.Auth{
		ID:       "auth2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test2@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth2); err != nil {
		t.Fatalf("manager.Register(auth2): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	registry.GetGlobalRegistry().RegisterClient(auth2.ID, auth2.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
		registry.GetGlobalRegistry().UnregisterClient(auth2.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 1,
		},
	}, manager)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}

	var gotErr error
	var gotStatus int
	for msg := range errChan {
		if msg != nil && msg.Error != nil {
			gotErr = msg.Error
			gotStatus = msg.StatusCode
		}
	}

	if string(got) != "partial" {
		t.Fatalf("expected payload partial, got %q", string(got))
	}
	if gotErr == nil {
		t.Fatalf("expected terminal error, got nil")
	}
	if gotStatus != http.StatusBadGateway {
		t.Fatalf("expected status %d, got %d", http.StatusBadGateway, gotStatus)
	}
	if executor.Calls() != 1 {
		t.Fatalf("expected 1 stream attempt, got %d", executor.Calls())
	}
}

func TestExecuteStreamWithAuthManager_EnrichesBootstrapRetryAuthUnavailableError(t *testing.T) {
	executor := &failOnceStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 1,
		},
	}, manager)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %q", string(got))
	}

	var gotErr *interfaces.ErrorMessage
	for msg := range errChan {
		if msg != nil {
			gotErr = msg
		}
	}
	if gotErr == nil {
		t.Fatalf("expected terminal error")
	}
	if gotErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", gotErr.StatusCode, http.StatusServiceUnavailable)
	}

	var authErr *coreauth.Error
	if !errors.As(gotErr.Error, &authErr) || authErr == nil {
		t.Fatalf("expected coreauth.Error, got %T", gotErr.Error)
	}
	if authErr.Code != "auth_unavailable" {
		t.Fatalf("code = %q, want %q", authErr.Code, "auth_unavailable")
	}
	if !strings.Contains(authErr.Message, "providers=codex") {
		t.Fatalf("message missing provider context: %q", authErr.Message)
	}
	if !strings.Contains(authErr.Message, "model=test-model") {
		t.Fatalf("message missing model context: %q", authErr.Message)
	}

	if executor.Calls() != 1 {
		t.Fatalf("expected exactly one upstream call before retry path selection failure, got %d", executor.Calls())
	}
}

func TestExecuteStreamWithAuthManager_PinnedAuthKeepsSameUpstream(t *testing.T) {
	executor := &authAwareStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	auth2 := &coreauth.Auth{
		ID:       "auth2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test2@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth2); err != nil {
		t.Fatalf("manager.Register(auth2): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	registry.GetGlobalRegistry().RegisterClient(auth2.ID, auth2.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
		registry.GetGlobalRegistry().UnregisterClient(auth2.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 1,
		},
	}, manager)
	ctx := WithPinnedAuthID(context.Background(), "auth1")
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(ctx, "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}

	var gotErr error
	for msg := range errChan {
		if msg != nil && msg.Error != nil {
			gotErr = msg.Error
		}
	}

	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %q", string(got))
	}
	if gotErr == nil {
		t.Fatalf("expected terminal error, got nil")
	}
	authIDs := executor.AuthIDs()
	if len(authIDs) == 0 {
		t.Fatalf("expected at least one upstream attempt")
	}
	for _, authID := range authIDs {
		if authID != "auth1" {
			t.Fatalf("expected all attempts on auth1, got sequence %v", authIDs)
		}
	}
}

func TestExecuteStreamWithAuthManager_SelectedAuthCallbackReceivesAuthID(t *testing.T) {
	executor := &authAwareStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth2 := &coreauth.Auth{
		ID:       "auth2",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test2@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth2); err != nil {
		t.Fatalf("manager.Register(auth2): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth2.ID, auth2.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth2.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{
		Streaming: sdkconfig.StreamingConfig{
			BootstrapRetries: 0,
		},
	}, manager)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	logging.SetGinRequestID(ginCtx, "1234abcd")

	selectedAuthID := ""
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	ctx = WithSelectedAuthIDCallback(ctx, func(authID string) {
		selectedAuthID = authID
	})
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(ctx, "openai", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}

	if string(got) != "ok" {
		t.Fatalf("expected payload ok, got %q", string(got))
	}
	if selectedAuthID != "auth2" {
		t.Fatalf("selectedAuthID = %q, want %q", selectedAuthID, "auth2")
	}
	traceID := logging.GetGinCPATraceID(ginCtx)
	parts := strings.Split(traceID, "-")
	if len(parts) != 3 || parts[1] != auth2.Index || parts[2] != "1234abcd" {
		t.Fatalf("trace ID = %q, want timestamp-%s-1234abcd", traceID, auth2.Index)
	}
	if _, errParse := time.Parse("20060102150405", parts[0]); errParse != nil {
		t.Fatalf("trace timestamp = %q: %v", parts[0], errParse)
	}
}

func TestExecuteStreamWithAuthManager_ValidatesOpenAIResponsesStreamDataJSON(t *testing.T) {
	executor := &invalidJSONStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai-response", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []byte
	for chunk := range dataChan {
		got = append(got, chunk...)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty payload, got %q", string(got))
	}

	gotErr := false
	for msg := range errChan {
		if msg == nil {
			continue
		}
		if msg.StatusCode != http.StatusBadGateway {
			t.Fatalf("expected status %d, got %d", http.StatusBadGateway, msg.StatusCode)
		}
		if msg.Error == nil {
			t.Fatalf("expected error")
		}
		gotErr = true
	}
	if !gotErr {
		t.Fatalf("expected terminal error")
	}
}

func TestExecuteStreamWithAuthManager_AllowsSplitOpenAIResponsesSSEEventLines(t *testing.T) {
	executor := &splitResponsesEventStreamExecutor{}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)

	auth1 := &coreauth.Auth{
		ID:       "auth1",
		Provider: "split-sse",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"email": "test1@example.com"},
	}
	if _, err := manager.Register(context.Background(), auth1); err != nil {
		t.Fatalf("manager.Register(auth1): %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth1.ID, auth1.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth1.ID)
	})

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	dataChan, _, errChan := handler.ExecuteStreamWithAuthManager(context.Background(), "openai-response", "test-model", []byte(`{"model":"test-model"}`), "")
	if dataChan == nil || errChan == nil {
		t.Fatalf("expected non-nil channels")
	}

	var got []string
	for chunk := range dataChan {
		got = append(got, string(chunk))
	}

	for msg := range errChan {
		if msg != nil {
			t.Fatalf("unexpected error: %+v", msg)
		}
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 forwarded chunks, got %d: %#v", len(got), got)
	}
	if got[0] != "event: response.completed" {
		t.Fatalf("unexpected first chunk: %q", got[0])
	}
	expectedData := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-1\",\"output\":[]}}"
	if got[1] != expectedData {
		t.Fatalf("unexpected second chunk.\nGot:  %q\nWant: %q", got[1], expectedData)
	}
}

func newBootstrapStreamTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader("{}"))
	return c, recorder
}

func TestBootstrapStreamHeartbeatsBeforeDelayedFirstChunk(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)
	interval := 10 * time.Millisecond

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			data := make(chan []byte, 1)
			errs := make(chan *interfaces.ErrorMessage)
			go func() {
				time.Sleep(60 * time.Millisecond)
				data <- []byte("hello")
				close(data)
				close(errs)
			}()
			return data, nil, errs
		},
		SetSSEHeaders: func() { c.Header("Content-Type", "text/event-stream") },
		OnFirstChunk: func(_ bool, _ http.Header, chunk []byte) {
			_, _ = c.Writer.Write([]byte("data: " + string(chunk) + "\n\n"))
		},
		Forward: func(data <-chan []byte, _ <-chan *interfaces.ErrorMessage) {
			for range data {
			}
		},
		Cancel: func(error) {},
	})

	body := recorder.Body.String()
	firstHeartbeat := strings.Index(body, KeepAliveSSEComment)
	firstPayload := strings.Index(body, "data: hello")
	if firstHeartbeat < 0 || firstPayload < 0 {
		t.Fatalf("body = %q, want heartbeat and payload", body)
	}
	if firstHeartbeat > firstPayload {
		t.Fatalf("body = %q, want heartbeat before payload", body)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestBootstrapStreamPreservesPassthroughHeadersInsteadOfHeartbeating(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{PassthroughHeaders: true}, nil)
	flusher := c.Writer.(http.Flusher)
	interval := 10 * time.Millisecond

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			time.Sleep(60 * time.Millisecond)
			data := make(chan []byte, 1)
			data <- []byte("hello")
			close(data)
			errs := make(chan *interfaces.ErrorMessage)
			close(errs)
			return data, http.Header{"X-Upstream-Request-Id": {"req-1"}}, errs
		},
		SetSSEHeaders: func() { c.Header("Content-Type", "text/event-stream") },
		OnFirstChunk: func(headersCommitted bool, upstreamHeaders http.Header, chunk []byte) {
			if headersCommitted {
				t.Fatal("passthrough stream committed headers before first payload")
			}
			WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
			_, _ = c.Writer.Write([]byte("data: " + string(chunk) + "\n\n"))
		},
		Forward: func(data <-chan []byte, _ <-chan *interfaces.ErrorMessage) {
			for range data {
			}
		},
		Cancel: func(error) {},
	})

	if strings.Contains(recorder.Body.String(), KeepAliveSSEComment) {
		t.Fatalf("unexpected heartbeat with passthrough headers: %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("X-Upstream-Request-Id"); got != "req-1" {
		t.Fatalf("X-Upstream-Request-Id = %q, want req-1", got)
	}
}

func TestBootstrapStreamHeartbeatsAcrossPreAcceptanceRetry(t *testing.T) {
	executor := &failOnceStreamExecutor{delay: 80 * time.Millisecond}
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(executor)
	for _, authID := range []string{"retry-auth-1", "retry-auth-2"} {
		auth := &coreauth.Auth{
			ID:       authID,
			Provider: "codex",
			Status:   coreauth.StatusActive,
			Metadata: map[string]any{"email": authID + "@example.com"},
		}
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("manager.Register(%s): %v", authID, err)
		}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
	}

	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	flusher := c.Writer.(http.Flusher)
	interval := 10 * time.Millisecond

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			return handler.ExecuteStreamWithAuthManager(context.Background(), "openai", "test-model", []byte(`{"model":"test-model"}`), "")
		},
		SetSSEHeaders: func() { c.Header("Content-Type", "text/event-stream") },
		OnFirstChunk: func(_ bool, _ http.Header, chunk []byte) {
			_, _ = c.Writer.Write([]byte("data: " + string(chunk) + "\n\n"))
		},
		Forward: func(data <-chan []byte, _ <-chan *interfaces.ErrorMessage) {
			for range data {
			}
		},
		Cancel: func(error) {},
	})

	body := recorder.Body.String()
	payload := strings.Index(body, "data: ok")
	if payload < 0 {
		t.Fatalf("body = %q, want retried payload; calls=%d", body, executor.Calls())
	}
	if beats := strings.Count(body[:payload], KeepAliveSSEComment); beats < 2 {
		t.Fatalf("body = %q, want heartbeats across retry; got %d", body, beats)
	}
	if executor.Calls() != 2 {
		t.Fatalf("stream attempts = %d, want 2", executor.Calls())
	}
}

func TestBootstrapStreamKeepsHTTPErrorBeforeFirstHeartbeat(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)
	interval := time.Second

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			errs := make(chan *interfaces.ErrorMessage, 1)
			errs <- &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("upstream failed")}
			close(errs)
			return nil, nil, errs
		},
		WriteUncommittedError: func(errMsg *interfaces.ErrorMessage) {
			c.JSON(errMsg.StatusCode, gin.H{"error": errMsg.Error.Error()})
		},
		Cancel: func(error) {},
	})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if strings.Contains(recorder.Body.String(), KeepAliveSSEComment) {
		t.Fatalf("unexpected heartbeat before immediate error: %q", recorder.Body.String())
	}
}

func TestBootstrapStreamDoesNotHeartbeatBeforeDelayedBootstrapError(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)
	interval := 10 * time.Millisecond

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			time.Sleep(60 * time.Millisecond)
			errs := make(chan *interfaces.ErrorMessage, 1)
			errs <- &interfaces.ErrorMessage{StatusCode: http.StatusServiceUnavailable, Error: errors.New("delayed bootstrap failure")}
			close(errs)
			return nil, nil, errs
		},
		SetSSEHeaders: func() { c.Header("Content-Type", "text/event-stream") },
		WriteUncommittedError: func(errMsg *interfaces.ErrorMessage) {
			c.JSON(errMsg.StatusCode, gin.H{"error": errMsg.Error.Error()})
		},
		Cancel: func(error) {},
	})

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), KeepAliveSSEComment) {
		t.Fatalf("unexpected heartbeat before bootstrap completed: %q", recorder.Body.String())
	}
}

func TestBootstrapStreamWritesInBandErrorAfterHeartbeat(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)
	interval := 10 * time.Millisecond

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		KeepAliveInterval: &interval,
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			data := make(chan []byte)
			errs := make(chan *interfaces.ErrorMessage, 1)
			go func() {
				time.Sleep(60 * time.Millisecond)
				errs <- &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("upstream failed")}
				close(errs)
			}()
			return data, nil, errs
		},
		SetSSEHeaders: func() { c.Header("Content-Type", "text/event-stream") },
		WriteCommittedError: func(errMsg *interfaces.ErrorMessage) []byte {
			body := []byte(`{"error":"` + errMsg.Error.Error() + `"}`)
			_, _ = c.Writer.Write([]byte("event: error\ndata: " + string(body) + "\n\n"))
			return body
		},
		Cancel: func(error) {},
	})

	body := recorder.Body.String()
	if !strings.Contains(body, KeepAliveSSEComment) || !strings.Contains(body, "event: error") {
		t.Fatalf("body = %q, want heartbeat and in-band error", body)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want committed %d", recorder.Code, http.StatusOK)
	}
	logged, exists := c.Get("API_RESPONSE")
	if !exists || !strings.Contains(string(logged.([]byte)), "upstream failed") {
		t.Fatalf("request log = %#v, want committed error", logged)
	}
}

func TestBootstrapStreamWaitsForTerminalErrorAfterDataCloses(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)
	closedCleanly := false

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			data := make(chan []byte)
			close(data)
			errs := make(chan *interfaces.ErrorMessage, 1)
			go func() {
				time.Sleep(30 * time.Millisecond)
				errs <- &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errors.New("late terminal error")}
				close(errs)
			}()
			return data, nil, errs
		},
		WriteUncommittedError: func(errMsg *interfaces.ErrorMessage) {
			c.JSON(errMsg.StatusCode, gin.H{"error": errMsg.Error.Error()})
		},
		OnStreamClosed:    func(bool, http.Header) { closedCleanly = true },
		Cancel:            func(error) {},
		DrainPendingError: true,
	})

	if closedCleanly {
		t.Fatal("stream closed cleanly before delayed terminal error")
	}
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if !strings.Contains(recorder.Body.String(), "late terminal error") {
		t.Fatalf("body = %q, want delayed terminal error", recorder.Body.String())
	}
}

func TestBootstrapStreamRejectsNilChannels(t *testing.T) {
	c, recorder := newBootstrapStreamTestContext()
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	flusher := c.Writer.(http.Flusher)

	handler.BootstrapStream(c, flusher, StreamBootstrapOptions{
		Execute: func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
			return nil, nil, nil
		},
		WriteUncommittedError: func(errMsg *interfaces.ErrorMessage) {
			c.JSON(errMsg.StatusCode, gin.H{"error": errMsg.Error.Error()})
		},
		Cancel: func(error) {},
	})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(recorder.Body.String(), errInvalidStreamBootstrap.Error()) {
		t.Fatalf("body = %q, want invalid bootstrap error", recorder.Body.String())
	}
}
