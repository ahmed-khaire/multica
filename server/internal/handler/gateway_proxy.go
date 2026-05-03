package handler

import "net/http"

func (h *Handler) GatewayOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeOpenAIChatCompletions(w, r)
}

func (h *Handler) GatewayAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeAnthropicMessages(w, r)
}

func (h *Handler) GatewayModels(w http.ResponseWriter, r *http.Request) {
	h.GatewayProxy.ServeModels(w, r)
}
