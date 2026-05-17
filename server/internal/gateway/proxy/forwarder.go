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

	start := time.Now()
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
		result, err := copyStreamingResponse(w, resp, summary.TranslationMode)
		result.StatusCode = resp.StatusCode
		result.Status = statusForHTTP(resp.StatusCode)
		result.ResponseHeaders = resp.Header.Clone()
		result.Streaming = true
		result.DurationMS = time.Since(start).Milliseconds()
		return result, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProxyResult{}, err
	}
	responseJSON := decodeObject(body)
	translationMode := normalizedTranslationMode(summary.TranslationMode)
	if translationMode != TranslationNone && len(body) > 0 {
		translated, decoded, err := TranslateResponseBody(body, translationMode)
		if err != nil {
			return ProxyResult{}, err
		}
		body = translated
		responseJSON = decoded
		w.Header().Del("Content-Length")
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return ProxyResult{}, err
		}
	}

	result := ProxyResult{
		StatusCode:      resp.StatusCode,
		Status:          statusForHTTP(resp.StatusCode),
		ResponseHeaders: resp.Header.Clone(),
		ResponseBody:    body,
		ResponseJSON:    responseJSON,
		DurationMS:      time.Since(start).Milliseconds(),
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
	body := summary.Body
	translationMode := normalizedTranslationMode(summary.TranslationMode)
	if translationMode != TranslationNone {
		translated, _, err := TranslateRequestBody(summary, translationMode)
		if err != nil {
			return nil, err
		}
		body = translated
	}

	upstreamURL := JoinUpstreamPath(target.BaseURL, summary.RoutePath)
	if inbound.URL.RawQuery != "" {
		upstreamURL += "?" + inbound.URL.RawQuery
	}

	var requestBody io.Reader
	if len(body) > 0 {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, upstreamURL, requestBody)
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

	upstreamProtocol := target.UpstreamProtocol
	if upstreamProtocol == "" {
		upstreamProtocol = summary.Protocol
	}
	switch upstreamProtocol {
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

func normalizedTranslationMode(mode TranslationMode) TranslationMode {
	if mode == "" {
		return TranslationNone
	}
	return mode
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

func copyStreamingResponse(w http.ResponseWriter, resp *http.Response, translationMode TranslationMode) (ProxyResult, error) {
	flusher, _ := w.(http.Flusher)
	start := time.Now()
	firstTokenMS := int64(0)
	chunks := 0
	if translationMode != TranslationNone {
		return copyTranslatedStreamingResponse(w, resp, flusher, translationMode, start)
	}
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

func copyTranslatedStreamingResponse(w http.ResponseWriter, resp *http.Response, flusher http.Flusher, translationMode TranslationMode, start time.Time) (ProxyResult, error) {
	firstTokenMS := int64(0)
	chunks := 0
	pending := []byte{}
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			frames, rest := splitSSEFrames(pending)
			pending = rest
			for _, frame := range frames {
				for _, translated := range TranslateStreamChunk(frame, translationMode) {
					if chunks == 0 {
						firstTokenMS = time.Since(start).Milliseconds()
					}
					chunks++
					if _, err := w.Write(translated); err != nil {
						return ProxyResult{Streaming: true, StreamingChunks: chunks, TimeToFirstTokenMS: firstTokenMS}, err
					}
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return ProxyResult{Streaming: true, StreamingChunks: chunks, TimeToFirstTokenMS: firstTokenMS}, readErr
		}
	}
	if len(strings.TrimSpace(string(pending))) > 0 {
		for _, translated := range TranslateStreamChunk(pending, translationMode) {
			if chunks == 0 {
				firstTokenMS = time.Since(start).Milliseconds()
			}
			chunks++
			if _, err := w.Write(translated); err != nil {
				return ProxyResult{Streaming: true, StreamingChunks: chunks, TimeToFirstTokenMS: firstTokenMS}, err
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	return ProxyResult{
		Streaming:          true,
		StreamingChunks:    chunks,
		TimeToFirstTokenMS: firstTokenMS,
	}, nil
}

func splitSSEFrames(in []byte) ([][]byte, []byte) {
	frames := [][]byte{}
	remaining := in
	for {
		idx := bytes.Index(remaining, []byte("\n\n"))
		if idx < 0 {
			break
		}
		end := idx + 2
		frame := append([]byte(nil), remaining[:end]...)
		frames = append(frames, frame)
		remaining = remaining[end:]
	}
	return frames, remaining
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
