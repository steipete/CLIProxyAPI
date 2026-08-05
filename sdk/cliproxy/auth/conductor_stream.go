package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func discardStreamChunks(ch <-chan cliproxyexecutor.StreamChunk) {
	if ch == nil {
		return
	}
	go func() {
		for range ch {
		}
	}()
}

type streamBootstrapError struct {
	cause            error
	headers          http.Header
	upstreamAccepted bool
}

func cloneHTTPHeader(headers http.Header) http.Header {
	if headers == nil {
		return nil
	}
	return headers.Clone()
}

func newStreamBootstrapError(err error, headers http.Header) error {
	return newStreamBootstrapErrorWithAcceptance(err, headers, false)
}

func newStreamBootstrapErrorWithAcceptance(err error, headers http.Header, upstreamAccepted bool) error {
	if err == nil {
		return nil
	}
	return &streamBootstrapError{
		cause:            err,
		headers:          cloneHTTPHeader(headers),
		upstreamAccepted: upstreamAccepted,
	}
}

func (e *streamBootstrapError) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e *streamBootstrapError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *streamBootstrapError) Headers() http.Header {
	if e == nil {
		return nil
	}
	return cloneHTTPHeader(e.headers)
}

func (e *streamBootstrapError) UpstreamAccepted() bool {
	return e != nil && e.upstreamAccepted
}

func upstreamAcceptedStreamError(err error) (*streamBootstrapError, bool) {
	var bootstrapErr *streamBootstrapError
	if !errors.As(err, &bootstrapErr) || bootstrapErr == nil || !bootstrapErr.UpstreamAccepted() {
		return nil, false
	}
	return bootstrapErr, true
}

func streamErrorResult(headers http.Header, err error, upstreamAccepted bool) *cliproxyexecutor.StreamResult {
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	ch <- cliproxyexecutor.StreamChunk{Err: err}
	close(ch)
	return &cliproxyexecutor.StreamResult{
		Headers:          cloneHTTPHeader(headers),
		Chunks:           ch,
		UpstreamAccepted: upstreamAccepted,
	}
}

func validateStreamResult(result *cliproxyexecutor.StreamResult, err error) (*cliproxyexecutor.StreamResult, error) {
	if err != nil {
		return result, err
	}
	if result == nil || result.Chunks == nil {
		return result, &Error{Code: "empty_stream", Message: "upstream stream has no source", Retryable: true}
	}
	return result, nil
}

func readStreamBootstrap(ctx context.Context, ch <-chan cliproxyexecutor.StreamChunk) ([]cliproxyexecutor.StreamChunk, bool, error) {
	if ch == nil {
		return nil, true, nil
	}
	buffered := make([]cliproxyexecutor.StreamChunk, 0, 1)
	for {
		var (
			chunk cliproxyexecutor.StreamChunk
			ok    bool
		)
		if ctx != nil {
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case chunk, ok = <-ch:
			}
		} else {
			chunk, ok = <-ch
		}
		if !ok {
			return buffered, true, nil
		}
		if chunk.Err != nil {
			return nil, false, chunk.Err
		}
		buffered = append(buffered, chunk)
		if len(chunk.Payload) > 0 {
			return buffered, false, nil
		}
	}
}

func (m *Manager) wrapStreamResult(ctx context.Context, cancel context.CancelFunc, auth *Auth, provider, resultModel string, upstreamAccepted bool, headers http.Header, buffered []cliproxyexecutor.StreamChunk, remaining <-chan cliproxyexecutor.StreamChunk, aliasResult OAuthModelAliasResult, ephemeralResult bool) *cliproxyexecutor.StreamResult {
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		if cancel != nil {
			defer cancel()
		}
		var failed bool
		forward := true
		var rewriter *StreamRewriter
		if aliasResult.ForceMapping && strings.TrimSpace(aliasResult.OriginalAlias) != "" {
			rewriter = NewStreamRewriter(StreamRewriteOptions{RewriteModel: aliasResult.OriginalAlias})
		}
		emit := func(chunk cliproxyexecutor.StreamChunk) bool {
			if chunk.Err != nil && !failed {
				failed = true
				rerr := resultErrorFromError(chunk.Err)
				m.recordExecutionResult(ctx, Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: false, Error: rerr}, auth, ephemeralResult)
			}
			if !forward {
				return false
			}
			if chunk.Err != nil {
				if ctx == nil {
					out <- chunk
					return true
				}
				select {
				case <-ctx.Done():
					forward = false
					return false
				case out <- chunk:
					return true
				}
			}
			if len(chunk.Payload) == 0 {
				return true
			}
			payload := rewriteForceMappedStreamChunk(rewriter, chunk.Payload)
			if len(payload) == 0 {
				return true
			}
			chunk.Payload = payload
			if ctx == nil {
				out <- chunk
				return true
			}
			select {
			case <-ctx.Done():
				forward = false
				return false
			case out <- chunk:
				return true
			}
		}
		for _, chunk := range buffered {
			if ok := emit(chunk); !ok {
				discardStreamChunks(remaining)
				return
			}
		}
		for chunk := range remaining {
			if ok := emit(chunk); !ok {
				discardStreamChunks(remaining)
				return
			}
		}
		if tail := finishForceMappedStreamChunks(rewriter); len(tail) > 0 {
			tailChunk := cliproxyexecutor.StreamChunk{Payload: tail}
			if !emit(tailChunk) {
				return
			}
		}
		if !failed && (ephemeralResult || claudeOAuthRequestCancellation(ctx, auth, nil) == nil) {
			m.recordExecutionResult(ctx, Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: true}, auth, ephemeralResult)
		}
	}()
	return &cliproxyexecutor.StreamResult{
		Headers:          headers,
		Chunks:           out,
		UpstreamAccepted: upstreamAccepted,
	}
}

func (m *Manager) replaceHomeExecutionLifecycleAuth(lifecycle cliproxyexecutor.ExecutionLifecycle, auth *Auth) {
	selection, ok := lifecycle.(*HomeDispatchSelection)
	if !ok || selection == nil {
		return
	}
	m.replaceHomeSelectionAuth(selection, auth)
}

func (m *Manager) executeStreamWithModelPool(ctx context.Context, executor ProviderExecutor, auth *Auth, provider string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, routeModel, executionModel string, execModels []string, pooled bool, aliasResult OAuthModelAliasResult, routing *apiKeyModelRoutingSnapshot, allowRetry bool, ephemeralResult bool, unauthorizedRefreshTried map[string]struct{}) (*cliproxyexecutor.StreamResult, error) {
	if executor == nil {
		return nil, &Error{Code: "executor_not_found", Message: "executor not registered"}
	}
	ctx = contextWithRequestedModelAlias(ctx, opts, routeModel)
	var lastErr error
	didRefreshOnUnauthorized := false
	if auth != nil && unauthorizedRefreshTried != nil {
		_, didRefreshOnUnauthorized = unauthorizedRefreshTried[auth.ID]
	}
	for idx, execModel := range execModels {
		resultModel := m.stateModelForExecution(auth, routeModel, execModel, pooled)
		execReq := req
		execReq.Model = execModel
		if executionModel != "" {
			execReq.Model = executionModel
		}
		execOpts := opts
		var errIntercept error
		execReq, execOpts, errIntercept = applyRequestAfterAuthInterceptor(ctx, executor, provider, execReq, execOpts, requestedModelAliasFromOptions(execOpts, routeModel))
		if errIntercept != nil {
			return nil, errIntercept
		}
		if executionModel == "" {
			execReq = attachResolvedAPIKeyModelInfo(routing, execReq, auth, routeModel, execModel)
		}
		if errCtx := ctx.Err(); errCtx != nil {
			return nil, errCtx
		}

		executeAttempt := func(currentAuth *Auth) (*cliproxyexecutor.StreamResult, context.CancelFunc, error) {
			attemptCtx := ctx
			cancelAttempt := context.CancelFunc(func() {})
			if ctx != nil {
				attemptCtx, cancelAttempt = context.WithCancel(ctx)
			}
			streamResult, errStream := executor.ExecuteStream(attemptCtx, currentAuth, execReq, execOpts)
			if errStream != nil {
				cancelAttempt()
				return nil, nil, errStream
			}
			return streamResult, cancelAttempt, nil
		}

		streamResult, cancelAttempt, errStream := executeAttempt(auth)
		if errStream != nil {
			if errCtx := ctx.Err(); errCtx != nil {
				return nil, errCtx
			}
			if allowRetry {
				alreadyTried := didRefreshOnUnauthorized
				willAttemptHomeRefresh := ephemeralResult && !alreadyTried && auth != nil && auth.AuthKind() == AuthKindOAuth && isUnauthorizedError(errStream)
				refreshed, okRefresh, errRefresh := m.tryRefreshExecutionAuthAfterUnauthorized(ctx, executor, auth, errStream, alreadyTried, ephemeralResult)
				if willAttemptHomeRefresh {
					didRefreshOnUnauthorized = true
					if unauthorizedRefreshTried != nil {
						unauthorizedRefreshTried[auth.ID] = struct{}{}
					}
				}
				if errRefresh != nil {
					errStream = errRefresh
				} else if okRefresh {
					auth = refreshed
					m.replaceHomeExecutionLifecycleAuth(execOpts.ExecutionLifecycle, auth)
					publishSelectedAuthMetadata(execOpts.Metadata, auth)
					didRefreshOnUnauthorized = true
					streamResult, cancelAttempt, errStream = executeAttempt(auth)
					if errStream != nil {
						if errCtx := ctx.Err(); errCtx != nil {
							return nil, errCtx
						}
					}
				}
			}
		}
		if !ephemeralResult {
			if errCancel := claudeOAuthRequestCancellation(ctx, auth, errStream); errCancel != nil {
				return nil, errCancel
			}
		}
		streamResult, errStream = validateStreamResult(streamResult, errStream)
		if errStream != nil {
			if cancelAttempt != nil {
				cancelAttempt()
			}
			rerr := resultErrorFromError(errStream)
			result := Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: false, Error: rerr}
			result.RetryAfter = retryAfterFromError(errStream)
			m.recordExecutionResult(ctx, result, auth, ephemeralResult)
			if isRequestInvalidError(errStream) {
				return nil, errStream
			}
			lastErr = errStream
			continue
		}

		if streamResult.UpstreamAccepted {
			attemptAliasResult := resolveAttemptAliasResult(routing, auth, routeModel, execModel, aliasResult)
			return m.wrapStreamResult(ctx, cancelAttempt, auth.Clone(), provider, resultModel, true, streamResult.Headers, nil, streamResult.Chunks, attemptAliasResult, ephemeralResult), nil
		}

		buffered, closed, bootstrapErr := readStreamBootstrap(ctx, streamResult.Chunks)
		if bootstrapErr != nil {
			if errCtx := ctx.Err(); errCtx != nil {
				if cancelAttempt != nil {
					cancelAttempt()
				}
				discardStreamChunks(streamResult.Chunks)
				return nil, errCtx
			}
			if allowRetry {
				alreadyTried := didRefreshOnUnauthorized
				willAttemptHomeRefresh := ephemeralResult && !alreadyTried && auth != nil && auth.AuthKind() == AuthKindOAuth && isUnauthorizedError(bootstrapErr)
				refreshed, okRefresh, errRefresh := m.tryRefreshExecutionAuthAfterUnauthorized(ctx, executor, auth, bootstrapErr, alreadyTried, ephemeralResult)
				if willAttemptHomeRefresh {
					didRefreshOnUnauthorized = true
					if unauthorizedRefreshTried != nil {
						unauthorizedRefreshTried[auth.ID] = struct{}{}
					}
				}
				if errRefresh != nil {
					if cancelAttempt != nil {
						cancelAttempt()
					}
					discardStreamChunks(streamResult.Chunks)
					bootstrapErr = errRefresh
					streamResult = &cliproxyexecutor.StreamResult{}
					cancelAttempt = nil
				} else if okRefresh {
					if cancelAttempt != nil {
						cancelAttempt()
					}
					discardStreamChunks(streamResult.Chunks)
					auth = refreshed
					m.replaceHomeExecutionLifecycleAuth(execOpts.ExecutionLifecycle, auth)
					publishSelectedAuthMetadata(execOpts.Metadata, auth)
					didRefreshOnUnauthorized = true
					retryStream, retryCancel, retryErr := executeAttempt(auth)
					retryStream, retryErr = validateStreamResult(retryStream, retryErr)
					if retryErr != nil {
						if retryCancel != nil {
							retryCancel()
						}
						if errCtx := ctx.Err(); errCtx != nil {
							return nil, errCtx
						}
						bootstrapErr = retryErr
						streamResult = &cliproxyexecutor.StreamResult{}
						cancelAttempt = nil
					} else {
						streamResult = retryStream
						cancelAttempt = retryCancel
						if streamResult.UpstreamAccepted {
							attemptAliasResult := resolveAttemptAliasResult(routing, auth, routeModel, execModel, aliasResult)
							return m.wrapStreamResult(ctx, cancelAttempt, auth.Clone(), provider, resultModel, true, streamResult.Headers, nil, streamResult.Chunks, attemptAliasResult, ephemeralResult), nil
						}
						buffered, closed, bootstrapErr = readStreamBootstrap(ctx, streamResult.Chunks)
					}
				}
			}
		}
		if !ephemeralResult {
			if errCancel := claudeOAuthRequestCancellation(ctx, auth, bootstrapErr); errCancel != nil {
				discardStreamChunks(streamResult.Chunks)
				return nil, errCancel
			}
		}
		if bootstrapErr != nil {
			rerr := resultErrorFromError(bootstrapErr)
			result := Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: false, Error: rerr}
			result.RetryAfter = retryAfterFromError(bootstrapErr)
			m.recordExecutionResult(ctx, result, auth, ephemeralResult)
			if cancelAttempt != nil {
				cancelAttempt()
			}
			discardStreamChunks(streamResult.Chunks)
			if streamResult.UpstreamAccepted {
				return nil, newStreamBootstrapErrorWithAcceptance(bootstrapErr, streamResult.Headers, true)
			}
			if isRequestInvalidError(bootstrapErr) {
				return nil, bootstrapErr
			}
			if idx < len(execModels)-1 {
				lastErr = bootstrapErr
				continue
			}
			return nil, newStreamBootstrapError(bootstrapErr, streamResult.Headers)
		}

		if closed && len(buffered) == 0 {
			emptyErr := &Error{Code: "empty_stream", Message: "upstream stream closed before first payload", Retryable: true}
			result := Result{AuthID: auth.ID, Provider: provider, Model: resultModel, Success: false, Error: emptyErr}
			m.recordExecutionResult(ctx, result, auth, ephemeralResult)
			if cancelAttempt != nil {
				cancelAttempt()
			}
			if streamResult.UpstreamAccepted {
				return nil, newStreamBootstrapErrorWithAcceptance(emptyErr, streamResult.Headers, true)
			}
			if idx < len(execModels)-1 {
				lastErr = emptyErr
				continue
			}
			return nil, newStreamBootstrapError(emptyErr, streamResult.Headers)
		}

		remaining := streamResult.Chunks
		if closed {
			closedCh := make(chan cliproxyexecutor.StreamChunk)
			close(closedCh)
			remaining = closedCh
		}
		attemptAliasResult := resolveAttemptAliasResult(routing, auth, routeModel, execModel, aliasResult)
		return m.wrapStreamResult(ctx, cancelAttempt, auth.Clone(), provider, resultModel, streamResult.UpstreamAccepted, streamResult.Headers, buffered, remaining, attemptAliasResult, ephemeralResult), nil
	}
	if lastErr == nil {
		lastErr = &Error{Code: "auth_not_found", Message: "no upstream model available"}
	}
	return nil, lastErr
}
