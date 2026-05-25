package main

import (
	"log/slog"
	"net/http"

	pluginroutes "github.com/Singleton-Solution/GoNext/apps/api/internal/plugins/routes"
	"github.com/Singleton-Solution/GoNext/packages/go/audit"
	hostbus "github.com/Singleton-Solution/GoNext/packages/go/hooks"
	"github.com/Singleton-Solution/GoNext/packages/go/plugins/capabilities"
	"github.com/Singleton-Solution/GoNext/packages/go/ratelimit"
)

// mountPluginRoutes wires the per-plugin HTTP route registry onto the
// shared mux (issue #136). It returns the Registry handle so the
// lifecycle Manager can Register / Unregister routes on activation.
//
// The Registry needs four collaborators:
//
//   - The shared hook bus, which is the synthetic-filter dispatcher
//     for inbound plugin requests. A nil bus disables the http.serve
//     feature — the mount returns nil and no routes are accepted at
//     Register.
//
//   - The audit emitter, threaded so every per-route invocation
//     leaves a trail.
//
//   - The capability registry, so the per-slug Checker built inside
//     the Registry can validate caps against the canonical catalog
//     (capabilities.Default).
//
//   - A rate limiter, scoped to "plugin:{slug}:http.serve". The
//     conservative default — 30 req/s steady-state with a 60 req
//     burst — is in line with the broader plugin-ABI policy of
//     containing one misbehaving plugin without taking the host
//     down. Operators can tune via configuration when the manifest
//     plumb-through arrives.
//
// mountPluginRoutes is intentionally permissive about which routes
// exist at boot: the Registry holds an empty map until the lifecycle
// Manager calls Register on the first Activate. We don't try to
// pre-populate from a persistence layer because plugin route
// configuration follows the plugin state — restart of the api
// triggers a full Activate replay through the lifecycle layer,
// which in turn re-registers routes here.
func mountPluginRoutes(mux *http.ServeMux, bus *hostbus.Bus, emitter *audit.Emitter, logger *slog.Logger) *pluginroutes.Registry {
	if mux == nil || bus == nil || emitter == nil {
		return nil
	}
	// Default per-plugin limiter: 60 burst capacity, 30 r/s steady
	// state. Same conservative envelope the doc places on outbound
	// http.fetch for inbound paths so a hot route can't pin the
	// process. Memory-backed because per-plugin keys are
	// process-local — there's no cross-replica coordination required
	// (each replica enforces its own quota; the aggregate is the
	// product of replicas, which matches every other API rate
	// limiter in the server).
	limiter, err := ratelimit.NewMemoryLimiter(ratelimit.Policy{
		Capacity:   60,
		RefillRate: 30,
	})
	if err != nil {
		logger.Warn("plugin_routes: rate limiter init failed; rate limiting disabled",
			slog.Any("err", err),
		)
		limiter = nil
	}
	reg, err := pluginroutes.NewRegistry(pluginroutes.Options{
		Mux:        mux,
		Dispatcher: pluginroutes.NewHookBusDispatcher(bus),
		Emitter:    emitter,
		Limiter:    limiter,
		CapReg:     capabilities.Default(),
		Logger:     logger,
	})
	if err != nil {
		logger.Warn("plugin_routes: registry init failed; http.serve disabled",
			slog.Any("err", err),
		)
		return nil
	}
	logger.Info("plugin_routes: http.serve registry mounted",
		slog.String("base", "/api/plugins/{slug}/"),
	)
	return reg
}
