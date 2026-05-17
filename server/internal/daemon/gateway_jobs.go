package daemon

import (
	"context"
	"fmt"
	"time"
)

const (
	gatewayJobTypeSubscriptionValidation = "gateway_subscription_validation"
	gatewayJobTypeRuntimeRequest         = "gateway_runtime_request"
)

func (d *Daemon) gatewayJobPollLoop(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, runtimeID := range d.allRuntimeIDs() {
				job, err := d.client.ClaimGatewayJob(ctx, runtimeID)
				if err != nil {
					d.logger.Warn("claim gateway job failed", "runtime_id", runtimeID, "error", err)
					continue
				}
				if job == nil {
					continue
				}
				d.logger.Info("gateway job received", "runtime_id", runtimeID, "job_id", job.ID, "type", job.Type)
				go d.handleGatewayJob(ctx, runtimeID, job)
			}
		}
	}
}

func (d *Daemon) handleGatewayJob(ctx context.Context, runtimeID string, job *GatewayJob) {
	switch job.Type {
	case gatewayJobTypeSubscriptionValidation:
		d.handleGatewayValidationJob(ctx, runtimeID, job)
	case gatewayJobTypeRuntimeRequest:
		d.handleGatewayRuntimeRequestJob(ctx, runtimeID, job)
	default:
		d.logger.Warn("unknown gateway job type", "type", job.Type, "job_id", job.ID)
	}
}

func (d *Daemon) handleGatewayValidationJob(ctx context.Context, runtimeID string, job *GatewayJob) {
	if err := d.validateGatewaySubscription(runtimeID, job); err != nil {
		_ = d.client.FailGatewayValidation(ctx, runtimeID, job.ID, "validation_failed", err.Error())
		return
	}
	_ = d.client.CompleteGatewayValidation(ctx, runtimeID, job.ID, GatewayValidationResult{
		AccountHint:        job.SubscriptionProvider,
		AccountFingerprint: fmt.Sprintf("%s:%s", job.SubscriptionProvider, runtimeID),
	})
}

func (d *Daemon) handleGatewayRuntimeRequestJob(ctx context.Context, runtimeID string, job *GatewayJob) {
	switch job.SubscriptionProvider {
	case "codex":
		response, err := d.executeCodexGatewayRuntimeRequest(ctx, job)
		if err != nil {
			_ = d.client.FailGatewayRuntimeRequest(ctx, runtimeID, job.ID, "runtime_error", err.Error())
			return
		}
		_ = d.client.CompleteGatewayRuntimeRequest(ctx, runtimeID, job.ID, response)
	default:
		_ = d.client.FailGatewayRuntimeRequest(ctx, runtimeID, job.ID, "unsupported_gateway_job", fmt.Sprintf("gateway runtime requests are not implemented for %s", job.SubscriptionProvider))
	}
}

func (d *Daemon) validateGatewaySubscription(runtimeID string, job *GatewayJob) error {
	rt := d.findRuntime(runtimeID)
	if rt == nil {
		return fmt.Errorf("runtime %s not found", runtimeID)
	}
	want := runtimeProviderForGatewaySubscription(job.SubscriptionProvider)
	if want == "" {
		return fmt.Errorf("unsupported subscription provider %q", job.SubscriptionProvider)
	}
	if rt.Provider != want {
		return fmt.Errorf("subscription provider %q requires runtime provider %q, got %q", job.SubscriptionProvider, want, rt.Provider)
	}
	return nil
}

func runtimeProviderForGatewaySubscription(provider string) string {
	switch provider {
	case "codex":
		return "codex"
	case "claude_code":
		return "claude"
	default:
		return ""
	}
}
