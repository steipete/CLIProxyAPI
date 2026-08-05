package handlers

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

var errInvalidStreamBootstrap = errors.New("stream bootstrap returned no data or error channel")

// StreamBootstrapOptions configures the protocol-specific parts of the first-event wait.
type StreamBootstrapOptions struct {
	KeepAliveInterval *time.Duration
	Execute           func() (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage)
	SetSSEHeaders     func()

	WriteCommittedError   func(errMsg *interfaces.ErrorMessage) []byte
	WriteUncommittedError func(errMsg *interfaces.ErrorMessage)
	OnFirstChunk          func(headersCommitted bool, upstreamHeaders http.Header, chunk []byte)
	OnStreamClosed        func(headersCommitted bool, upstreamHeaders http.Header)
	Forward               func(data <-chan []byte, errs <-chan *interfaces.ErrorMessage)
	Cancel                func(error)
	DrainPendingError     bool
}

type streamBootstrapResult struct {
	data    <-chan []byte
	headers http.Header
	errs    <-chan *interfaces.ErrorMessage
}

// BootstrapStream keeps the downstream SSE connection alive while the upstream selects
// credentials, retries a pre-acceptance failure, or waits for its first event.
func (h *BaseAPIHandler) BootstrapStream(c *gin.Context, flusher http.Flusher, opts StreamBootstrapOptions) {
	if c == nil || flusher == nil || opts.Execute == nil || opts.Cancel == nil {
		return
	}

	keepAliveInterval := StreamingKeepAliveInterval(h.Cfg)
	if opts.KeepAliveInterval != nil {
		keepAliveInterval = *opts.KeepAliveInterval
	}
	// A heartbeat commits HTTP 200 before delayed upstream/plugin headers are known.
	// Preserve opt-in response-header contracts instead of silently dropping those headers.
	if PassthroughHeadersEnabled(h.Cfg) || streamInterceptorsEnabled(h.interceptorHost()) {
		keepAliveInterval = 0
	}

	var (
		dataChan        <-chan []byte
		errChan         <-chan *interfaces.ErrorMessage
		upstreamHeaders http.Header
		resultChan      <-chan streamBootstrapResult
		keepAlive       *time.Ticker
		keepAliveC      <-chan time.Time
	)
	defer func() {
		if keepAlive != nil {
			keepAlive.Stop()
		}
	}()

	if keepAliveInterval > 0 {
		results := make(chan streamBootstrapResult, 1)
		resultChan = results
		go func() {
			data, headers, errs := opts.Execute()
			results <- streamBootstrapResult{data: data, headers: headers, errs: errs}
		}()
	} else {
		dataChan, upstreamHeaders, errChan = opts.Execute()
	}

	headersCommitted := false

	writeError := func(errMsg *interfaces.ErrorMessage) {
		if errMsg == nil {
			errMsg = executionErrorMessage(errInvalidStreamBootstrap)
		}
		if headersCommitted {
			if opts.WriteCommittedError != nil {
				if body := opts.WriteCommittedError(errMsg); len(body) > 0 {
					appendAPIResponse(c, body)
				}
				flusher.Flush()
			}
		} else if opts.WriteUncommittedError != nil {
			opts.WriteUncommittedError(errMsg)
		}
		opts.Cancel(errMsg.Error)
	}

	finishClosed := func() {
		if opts.OnStreamClosed != nil {
			opts.OnStreamClosed(headersCommitted, upstreamHeaders)
		}
		flusher.Flush()
		opts.Cancel(nil)
	}

	bindResult := func(result streamBootstrapResult) bool {
		dataChan, upstreamHeaders, errChan = result.data, result.headers, result.errs
		resultChan = nil
		if keepAliveInterval > 0 && keepAlive == nil {
			keepAlive = time.NewTicker(keepAliveInterval)
			keepAliveC = keepAlive.C
		}
		if dataChan == nil && errChan == nil {
			writeError(nil)
			return true
		}
		return false
	}

	handleError := func(errMsg *interfaces.ErrorMessage, ok bool) bool {
		if !ok {
			errChan = nil
			if dataChan == nil {
				finishClosed()
				return true
			}
			return false
		}
		writeError(errMsg)
		return true
	}

	handleData := func(chunk []byte, ok bool) bool {
		if !ok {
			if opts.DrainPendingError && errChan != nil {
				for {
					select {
					case <-c.Request.Context().Done():
						opts.Cancel(c.Request.Context().Err())
						return true
					case errMsg, errOK := <-errChan:
						if errOK {
							writeError(errMsg)
							return true
						}
						errChan = nil
						finishClosed()
						return true
					}
				}
			}
			finishClosed()
			return true
		}
		if opts.OnFirstChunk != nil {
			opts.OnFirstChunk(headersCommitted, upstreamHeaders, chunk)
		}
		if opts.Forward != nil {
			opts.Forward(dataChan, errChan)
		}
		return true
	}

	if resultChan == nil && dataChan == nil && errChan == nil {
		writeError(nil)
		return
	}

	for {
		select {
		case <-c.Request.Context().Done():
			opts.Cancel(c.Request.Context().Err())
			return
		case result := <-resultChan:
			if bindResult(result) {
				return
			}
		case <-keepAliveC:
			// If bootstrap work completed at the heartbeat boundary, preserve its real
			// status or first payload instead of nondeterministically committing HTTP 200.
			if resultChan != nil {
				select {
				case result := <-resultChan:
					if bindResult(result) {
						return
					}
				default:
				}
			}
			if dataChan != nil {
				select {
				case chunk, ok := <-dataChan:
					if handleData(chunk, ok) {
						return
					}
				default:
				}
			}
			if errChan != nil {
				select {
				case errMsg, ok := <-errChan:
					if handleError(errMsg, ok) {
						return
					}
				default:
				}
			}
			if !headersCommitted {
				if opts.SetSSEHeaders != nil {
					opts.SetSSEHeaders()
				}
				headersCommitted = true
			}
			_, _ = c.Writer.Write([]byte(KeepAliveSSEComment))
			flusher.Flush()
		case errMsg, ok := <-errChan:
			if handleError(errMsg, ok) {
				return
			}
		case chunk, ok := <-dataChan:
			if handleData(chunk, ok) {
				return
			}
		}
	}
}
