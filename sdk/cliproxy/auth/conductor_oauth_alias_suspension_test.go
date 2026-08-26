package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

type aliasRoutingExecutor struct {
	id string

	mu             sync.Mutex
	executeModels  []string
	executeAliases []string
}

func (e *aliasRoutingExecutor) Identifier() string { return e.id }

func (e *aliasRoutingExecutor) Execute(ctx context.Context, _ *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.mu.Lock()
	e.executeModels = append(e.executeModels, req.Model)
	e.executeAliases = append(e.executeAliases, coreusage.RequestedModelAliasFromContext(ctx))
	e.mu.Unlock()
	return cliproxyexecutor.Response{Payload: []byte(req.Model)}, nil
}

func (e *aliasRoutingExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "ExecuteStream not implemented"}
}

func (e *aliasRoutingExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *aliasRoutingExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusNotImplemented, Message: "CountTokens not implemented"}
}

func (e *aliasRoutingExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, &Error{HTTPStatus: http.StatusNotImplemented, Message: "HttpRequest not implemented"}
}

func (e *aliasRoutingExecutor) ExecuteModels() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeModels))
	copy(out, e.executeModels)
	return out
}

func (e *aliasRoutingExecutor) ExecuteAliases() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.executeAliases))
	copy(out, e.executeAliases)
	return out
}

func TestManagerExecute_OAuthAliasBypassesBlockedRouteModel(t *testing.T) {
	const (
		provider    = "antigravity"
		routeModel  = "claude-opus-4-6"
		targetModel = "claude-opus-4-6-thinking"
	)

	manager := NewManager(nil, nil, nil)
	executor := &aliasRoutingExecutor{id: provider}
	manager.RegisterExecutor(executor)
	manager.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{
		provider: {{
			Name:  targetModel,
			Alias: routeModel,
			Fork:  true,
		}},
	})

	auth := &Auth{
		ID:       "oauth-alias-auth",
		Provider: provider,
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			routeModel: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: time.Now().Add(1 * time.Hour),
			},
		},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, provider, []*registry.ModelInfo{{ID: routeModel}, {ID: targetModel}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth.ID)
	})
	manager.RefreshSchedulerEntry(auth.ID)

	resp, errExecute := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: routeModel}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("execute error = %v, want success", errExecute)
	}
	if string(resp.Payload) != targetModel {
		t.Fatalf("execute payload = %q, want %q", string(resp.Payload), targetModel)
	}

	gotModels := executor.ExecuteModels()
	if len(gotModels) != 1 {
		t.Fatalf("execute models len = %d, want 1", len(gotModels))
	}
	if gotModels[0] != targetModel {
		t.Fatalf("execute model = %q, want %q", gotModels[0], targetModel)
	}

	gotAliases := executor.ExecuteAliases()
	if len(gotAliases) != 1 {
		t.Fatalf("execute aliases len = %d, want 1", len(gotAliases))
	}
	if gotAliases[0] != routeModel {
		t.Fatalf("execute alias = %q, want %q", gotAliases[0], routeModel)
	}
}

func TestManagerExecute_HiddenOAuthAliasStaysCredentialScoped(t *testing.T) {
	const (
		provider    = "codex"
		hiddenModel = "hidden-codex-model"
		aliasModel  = "codex-latest"
		templateID  = "gpt-5.6-sol"
	)
	manager := NewManager(nil, nil, nil)
	executor := &forceMappingExecutor{id: provider}
	manager.RegisterExecutor(executor)

	owner := &Auth{
		ID:       "hidden-oauth-routing-owner",
		Provider: provider,
		Status:   StatusActive,
		Attributes: map[string]string{
			AttributeAuthKind:             AuthKindOAuth,
			oauthModelAliasesAttributeKey: `[{"name":"hidden-codex-model","alias":"codex-latest","template":"gpt-5.6-sol","force-mapping":true}]`,
		},
	}
	other := &Auth{
		ID:         "hidden-oauth-routing-other",
		Provider:   provider,
		Status:     StatusActive,
		Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth},
	}
	for _, credential := range []*Auth{other, owner} {
		if _, errRegister := manager.Register(context.Background(), credential); errRegister != nil {
			t.Fatalf("register auth %q: %v", credential.ID, errRegister)
		}
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(owner.ID, provider, []*registry.ModelInfo{{ID: templateID}, {ID: aliasModel}})
	reg.RegisterClient(other.ID, provider, []*registry.ModelInfo{{ID: templateID}})
	t.Cleanup(func() {
		reg.UnregisterClient(owner.ID)
		reg.UnregisterClient(other.ID)
	})
	manager.SetValidatedOAuthModelAliases(owner.ID, []internalconfig.OAuthModelAlias{{
		Name: hiddenModel, Alias: aliasModel, Template: templateID,
	}})
	manager.RefreshSchedulerEntry(owner.ID)
	manager.RefreshSchedulerEntry(other.ID)

	resp, errExecute := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: aliasModel}, cliproxyexecutor.Options{})
	if errExecute != nil {
		t.Fatalf("execute hidden alias: %v", errExecute)
	}
	if got := executor.ExecuteModels(); len(got) != 1 || got[0] != hiddenModel {
		t.Fatalf("upstream execution models = %v, want [%s]", got, hiddenModel)
	}
	if got := string(resp.Payload); !strings.Contains(got, aliasModel) || strings.Contains(got, hiddenModel) {
		t.Fatalf("response = %s, want public alias without hidden source", got)
	}

	stream, errStream := manager.ExecuteStream(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: aliasModel}, cliproxyexecutor.Options{})
	if errStream != nil {
		t.Fatalf("stream hidden alias: %v", errStream)
	}
	var streamed strings.Builder
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk: %v", chunk.Err)
		}
		streamed.Write(chunk.Payload)
	}
	if got := streamed.String(); !strings.Contains(got, aliasModel) || strings.Contains(got, hiddenModel) {
		t.Fatalf("stream response = %s, want public alias without hidden source", got)
	}
	if got := executor.StreamModels(); len(got) != 1 || got[0] != hiddenModel {
		t.Fatalf("stream upstream models = %v, want [%s]", got, hiddenModel)
	}

	if _, errHidden := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: hiddenModel}, cliproxyexecutor.Options{}); errHidden == nil {
		t.Fatal("direct hidden source request unexpectedly found an eligible credential")
	}
}

func TestManagerExecute_HiddenOAuthAliasWithoutRegisteredTemplateFailsClosed(t *testing.T) {
	const provider = "codex"
	manager := NewManager(nil, nil, nil)
	executor := &aliasRoutingExecutor{id: provider}
	manager.RegisterExecutor(executor)
	manager.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{
		provider: {{Name: "hidden-codex-model", Alias: "codex-latest", Template: "missing-template"}},
	})
	credential := &Auth{ID: "hidden-oauth-invalid-template", Provider: provider, Status: StatusActive}
	if _, errRegister := manager.Register(context.Background(), credential); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(credential.ID, provider, []*registry.ModelInfo{{ID: "gpt-5.6-sol"}})
	t.Cleanup(func() { reg.UnregisterClient(credential.ID) })
	manager.RefreshSchedulerEntry(credential.ID)

	if _, errExecute := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: "codex-latest"}, cliproxyexecutor.Options{}); errExecute == nil {
		t.Fatal("unknown template alias unexpectedly routed")
	}
	if got := executor.ExecuteModels(); len(got) != 0 {
		t.Fatalf("unexpected upstream executions: %v", got)
	}
}

func TestManagerExecute_InvalidHiddenOAuthAliasCannotHijackNativeModel(t *testing.T) {
	const (
		provider      = "codex"
		hiddenModel   = "hidden-codex-model"
		aliasModel    = "codex-latest"
		templateModel = "gpt-5.6-sol"
		ordinaryAlias = "codex-visible"
	)
	tests := []struct {
		name     string
		template string
		perAuth  bool
	}{
		{name: "global native alias collision", template: templateModel},
		{name: "global unknown template", template: "missing-template"},
		{name: "per-auth native alias collision", template: templateModel, perAuth: true},
		{name: "per-auth unknown template", template: "missing-template", perAuth: true},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			manager := NewManager(nil, nil, nil)
			executor := &aliasRoutingExecutor{id: provider}
			manager.RegisterExecutor(executor)
			ordinary := internalconfig.OAuthModelAlias{Name: templateModel, Alias: ordinaryAlias}
			hidden := internalconfig.OAuthModelAlias{Name: hiddenModel, Alias: aliasModel, Template: testCase.template, ForceMapping: true}
			global := []internalconfig.OAuthModelAlias{ordinary}
			if !testCase.perAuth {
				global = append(global, hidden)
			}
			manager.SetOAuthModelAlias(map[string][]internalconfig.OAuthModelAlias{provider: global})
			credential := &Auth{
				ID:         "invalid-hidden-oauth-alias-" + string(rune('a'+index)),
				Provider:   provider,
				Status:     StatusActive,
				Attributes: map[string]string{AttributeAuthKind: AuthKindOAuth},
			}
			if testCase.perAuth {
				SetOAuthModelAliasesAttribute(credential, []internalconfig.OAuthModelAlias{hidden})
			}
			if _, errRegister := manager.Register(context.Background(), credential); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}
			reg := registry.GetGlobalRegistry()
			reg.RegisterClient(credential.ID, provider, []*registry.ModelInfo{
				{ID: aliasModel}, {ID: templateModel}, {ID: ordinaryAlias},
			})
			t.Cleanup(func() { reg.UnregisterClient(credential.ID) })
			manager.RefreshSchedulerEntry(credential.ID)

			if got := manager.ResolveExecutionModel(credential, aliasModel); got != aliasModel {
				t.Fatalf("invalid alias rewrote %q to %q", aliasModel, got)
			}
			if _, errSelect := manager.SelectAuth(context.Background(), provider, aliasModel, cliproxyexecutor.Options{}); errSelect == nil {
				t.Fatal("invalid alias remained eligible for credential selection")
			}
			if _, errExecute := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: aliasModel}, cliproxyexecutor.Options{}); errExecute == nil {
				t.Fatal("invalid alias unexpectedly executed")
			}
			if _, errStream := manager.ExecuteStream(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: aliasModel}, cliproxyexecutor.Options{}); errStream == nil {
				t.Fatal("invalid alias unexpectedly started streaming")
			}
			if got := executor.ExecuteModels(); len(got) != 0 {
				t.Fatalf("invalid alias reached upstream: %v", got)
			}

			if _, errExecute := manager.Execute(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: ordinaryAlias}, cliproxyexecutor.Options{}); errExecute != nil {
				t.Fatalf("ordinary OAuth alias stopped working: %v", errExecute)
			}
			if got := executor.ExecuteModels(); len(got) != 1 || got[0] != templateModel {
				t.Fatalf("ordinary alias execution = %v, want [%s]", got, templateModel)
			}
		})
	}
}
