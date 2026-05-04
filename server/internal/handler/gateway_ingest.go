package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/multica-ai/multica/server/internal/gateway/ingest"
	"github.com/multica-ai/multica/server/internal/gateway/proxy"
)

const gatewayTraceIngestMaxBodyBytes = 4 << 20

func (h *Handler) PostGatewayTraceIngest(w http.ResponseWriter, r *http.Request) {
	gatewayKey, ok := ingest.ExtractTraceIngestKey(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "gateway key is required")
		return
	}

	var req ingest.TraceRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, gatewayTraceIngestMaxBodyBytes))
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resp, err := h.GatewayIngest.IngestTrace(r.Context(), gatewayKey, req)
	if err == nil {
		writeJSON(w, http.StatusCreated, resp)
		return
	}

	switch {
	case errors.Is(err, proxy.ErrGatewayKeyRequired), errors.Is(err, proxy.ErrGatewayKeyInvalid):
		writeError(w, http.StatusUnauthorized, "gateway key is invalid")
	case errors.Is(err, ingest.ErrInvalidTracePayload):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("gateway trace ingest failed", "error", err)
		writeError(w, http.StatusInternalServerError, "gateway trace ingest failed")
	}
}
