package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultAnthropicVersion = "2023-06-01"

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

type Forwarder struct {
	Client *http.Client
}

func NewForwarder(client *http.Client) *Forwarder {
	if client == nil {
		client = http.DefaultClient
	}
	return &Forwarder{Client: client}
}

func (f *Forwarder) Forward(ctx context.Context, w http.ResponseWriter, r *http.Request, target BackendTarget, summary RequestSummary) (ProxyResult, error) {
	upstreamReq, err := BuildUpstreamRequest(ctx, r, target, summary)
	if err != nil {
		return ProxyResult{}, err
	}

	resp, err := f.Client.Do(upstreamReq)
	if err != nil {
		return ProxyResult{
			Status:       StatusGatewayError,
			ErrorType:    "upstream_request_error",
			ErrorMessage: err.Error(),
		}, err
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	isStream := summary.Stream || strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	if isStream {
		result, err := copyStreamingResponse(w, resp)
		result.StatusCode = resp.StatusCode
		result.Status = statusForHTTP(resp.StatusCode)
		result.Streaming = true
		return result, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProxyResult{}, err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return ProxyResult{}, err
		}
	}

	result := ProxyResult{
		StatusCode:      resp.StatusCode,
		Status:          statusForHTTP(resp.StatusCode),
		ResponseBody:    body,
		ResponseJSON:    decodeObject(body),
		Streaming:       false,
		ErrorType:       errorTypeForHTTP(resp.StatusCode),
		ErrorMessage:    "",
		StreamingChunks: 0,
	}
	if result.Status != StatusSuccess && len(body) > 0 {
		result.ErrorMessage = string(body)
	}
	return result, nil
}

func BuildUpstreamRequest(ctx context.Context, inbound *http.Request, target BackendTarget, summary RequestSummary) (*http.Request, error) {
	method := summary.Method
	if method == "" {
		method = inbound.Method
	}

	upstreamURL := JoinUpstreamPath(target.BaseURL, summary.RoutePath)
	if inbound.URL.RawQuery != "" {
		upstreamURL += "?" + inbound.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, method, upstreamURL, bytes.NewReader(summary.Body))
	if err != nil {
		return nil, err
	}
	req.Header = cloneForwardHeaders(inbound.Header)
	req.Header.Del("Authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("X-Workspace-ID")
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-multica-") {
			req.Header.Del(name)
		}
	}

	switch summary.Protocol {
	case ProtocolAnthropic:
		req.Header.Set("x-api-key", target.UpstreamSecret)
		if req.Header.Get("anthropic-version") == "" {
			req.Header.Set("anthropic-version", defaultAnthropicVersion)
		}
	default:
		req.Header.Set("Authorization", "Bearer "+target.UpstreamSecret)
	}

	return req, nil
}

func cloneForwardHeaders(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		if _, drop := hopByHopHeaders[canonical]; drop {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
	return dst
}

func copyResponseHeaders(dst, src http.Header) {
	for name, values := range src {
		canonical := http.CanonicalHeaderKey(name)
		if _, drop := hopByHopHeaders[canonical]; drop {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func copyStreamingResponse(w http.ResponseWriter, resp *http.Response) (ProxyResult, error) {
	flusher, _ := w.(http.Flusher)
	start := time.Now()
	firstTokenMS := int64(0)
	chunks := 0
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if chunks == 0 {
				firstTokenMS = time.Since(start).Milliseconds()
			}
			chunks++
			if _, err := w.Write(buf[:n]); err != nil {
				return ProxyResult{Streaming: true, StreamingChunks: chunks, TimeToFirstTokenMS: firstTokenMS}, err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return ProxyResult{Streaming: true, StreamingChunks: chunks, TimeToFirstTokenMS: firstTokenMS}, readErr
		}
	}
	return ProxyResult{
		Streaming:          true,
		StreamingChunks:    chunks,
		TimeToFirstTokenMS: firstTokenMS,
	}, nil
}

func statusForHTTP(status int) string {
	if status >= 200 && status < 400 {
		return StatusSuccess
	}
	return StatusUpstreamError
}

func errorTypeForHTTP(status int) string {
	if status >= 200 && status < 400 {
		return ""
	}
	return "upstream_error"
}

func decodeObject(body []byte) map[string]any {
	if len(body) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil
	}
	return out
}
