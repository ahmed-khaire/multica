package proxy

import (
	"fmt"
	"strings"
)

const (
	RoutingSourceDefault              = "default"
	RoutingSourceHeader               = "header"
	RoutingSourceModelPrefix          = "model_prefix"
	RoutingSourceHeaderAndModelPrefix = "header_and_model_prefix"
)

func ParseModelRouting(model, headerBackend string) (ModelRouting, error) {
	requestedModel := strings.TrimSpace(model)
	headerBackend = strings.TrimSpace(headerBackend)

	routing := ModelRouting{
		RequestedModel: requestedModel,
		ForwardedModel: requestedModel,
		BackendSlug:    headerBackend,
		Source:         RoutingSourceDefault,
	}
	if headerBackend != "" {
		routing.Source = RoutingSourceHeader
	}

	prefix, suffix, ok := splitProviderModel(requestedModel)
	if !ok {
		return routing, nil
	}

	if headerBackend != "" && headerBackend != prefix {
		return ModelRouting{}, fmt.Errorf("%w: header backend %q conflicts with model backend %q", ErrBackendRoutingConflict, headerBackend, prefix)
	}

	routing.BackendSlug = prefix
	routing.ForwardedModel = suffix
	if headerBackend != "" {
		routing.Source = RoutingSourceHeaderAndModelPrefix
	} else {
		routing.Source = RoutingSourceModelPrefix
	}
	return routing, nil
}

func splitProviderModel(model string) (string, string, bool) {
	before, after, found := strings.Cut(model, ":")
	if !found {
		return "", "", false
	}
	before = strings.TrimSpace(before)
	after = strings.TrimSpace(after)
	if before == "" || after == "" {
		return "", "", false
	}
	return before, after, true
}
