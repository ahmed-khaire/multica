package handler

import "net/http"

func (h *Handler) GatewayOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeOpenAIChatCompletions(w, r)
}

func (h *Handler) GatewayOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeOpenAIResponses(w, r)
}

func (h *Handler) GatewayAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeAnthropicMessages(w, r)
}

func (h *Handler) GatewayAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeAnthropicCountTokens(w, r)
}

func (h *Handler) GatewayModels(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeModels(w, r)
}
