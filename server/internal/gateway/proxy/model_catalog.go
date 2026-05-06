package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"

	"github.com/multica-ai/multica/server/internal/util"
)

type CatalogModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type CatalogResponse struct {
	Object string         `json:"object"`
	Data   []CatalogModel `json:"data"`
}

func (s *Service) serveModels(w http.ResponseWriter, r *http.Request, authCtx AuthContext, protocol string) {
	workspaceID, err := parseUUID(authCtx.WorkspaceID)
	if err != nil {
		writeProviderError(w, protocol, RoutingError(http.StatusBadRequest, "invalid workspace", "invalid_workspace", err))
		return
	}

	settings, _ := s.Queries.GetGatewayWorkspaceSettings(r.Context(), workspaceID)
	defaultBackendID := ""
	if settings.DefaultBackendID.Valid {
		defaultBackendID = util.UUIDToString(settings.DefaultBackendID)
	}

	backends, err := s.Queries.ListEnabledGatewayBackends(r.Context(), workspaceID)
	if err != nil {
		writeProviderError(w, protocol, RoutingError(http.StatusInternalServerError, "gateway model catalog failed", "gateway_model_catalog_failed", err))
		return
	}

	models := map[string]CatalogModel{}
	for _, backend := range backends {
		target, err := s.Resolver.ResolveBackend(r.Context(), authCtx.WorkspaceID, protocol, backend.Slug)
		if err != nil {
			continue
		}
		ids, err := s.fetchBackendModelIDs(r.Context(), r, target, protocol)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if id == "" {
				continue
			}
			if target.ID == defaultBackendID {
				models[id] = CatalogModel{ID: id, Object: "model", OwnedBy: target.Slug}
			}
			prefixedID := target.Slug + ":" + id
			models[prefixedID] = CatalogModel{ID: prefixedID, Object: "model", OwnedBy: target.Slug}
		}
	}

	resp := CatalogResponse{Object: "list", Data: make([]CatalogModel, 0, len(models))}
	for _, model := range models {
		resp.Data = append(resp.Data, model)
	}
	sort.Slice(resp.Data, func(i, j int) bool {
		return resp.Data[i].ID < resp.Data[j].ID
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Service) fetchBackendModelIDs(ctx context.Context, inbound *http.Request, target BackendTarget, protocol string) ([]string, error) {
	upstreamReq, err := BuildUpstreamRequest(ctx, inbound, target, RequestSummary{
		Protocol:  protocol,
		RoutePath: routePathFor(SurfaceModels, target),
		Method:    http.MethodGet,
	})
	if err != nil {
		return nil, err
	}

	resp, err := s.Forwarder.Client.Do(upstreamReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errors.New("upstream model catalog failed")
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return extractCatalogModelIDs(body)
}

func extractCatalogModelIDs(body []byte) ([]string, error) {
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(payload.Data))
	for _, item := range payload.Data {
		id, _ := item["id"].(string)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
