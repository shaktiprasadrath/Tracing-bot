package api

import (
	"context"
	"errors"
	"net"
	"net/http"

	"traceiq/web"
)

// NewHTTPServer builds a fully wired *HTTPServer: mux, middleware chain and
// embedded SPA. Deps fields left nil degrade their own handlers to 503
// (see handlers.go/mcp.go) rather than panicking, so a partial wiring is
// still a usable process for the routes that ARE configured.
func NewHTTPServer(addr string, deps Deps) *HTTPServer {
	s := &HTTPServer{Deps: deps, addr: addr}
	s.mux = s.routes()
	return s
}

// routes builds the request mux: REST/JSON under /v1 (DR-29 §29.1's table,
// the subset this wave implements — see docs/reports/w15-api-web.md for the
// rest), the MCP JSON-RPC endpoint (DR-29 §29.2), health probes
// (unauthenticated per F12 §4.3), and the embedded SPA at "/" with a
// History-API fallback to index.html for unmatched client-side routes
// (FR-F12-1/FR-F12-2).
func (s *HTTPServer) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", unauthenticated(s.handleHealthz))
	mux.HandleFunc("GET /readyz", unauthenticated(s.handleReadyz))

	mux.HandleFunc("GET /v1/overview", s.requireCapability(CapTelemetryRead, s.handleOverview))
	mux.HandleFunc("GET /v1/traces", s.requireCapability(CapTelemetryRead, s.handleTracesSearch))
	mux.HandleFunc("GET /v1/traces/{id}", s.requireCapability(CapTelemetryRead, s.handleTraceGet))
	mux.HandleFunc("GET /v1/topology", s.requireCapability(CapTelemetryRead, s.handleTopology))
	mux.HandleFunc("GET /v1/incidents", s.requireCapability(CapTelemetryRead, s.handleIncidents))

	mux.HandleFunc("GET /v1/investigations/{id}", s.requireCapability(CapTelemetryRead, s.handleInvestigationGet))
	mux.HandleFunc("POST /v1/investigations/{id}/replay", s.requireCapability(CapInvestigationReplay, s.handleInvestigationReplay))
	mux.HandleFunc("POST /v1/investigations/{id}/steps/{stepID}/correct", s.requireCapability(CapInvestigationCorrect, s.handleInvestigationCorrect))

	mux.HandleFunc("GET /v1/remediation/actions", s.requireCapability(CapTelemetryRead, s.handleActionsList))
	mux.HandleFunc("POST /v1/actions", s.requireCapability(CapRemediatePropose, s.handleActionsPropose))
	mux.HandleFunc("POST /v1/actions/{id}/approve", s.requireCapability(CapRemediateApprove, s.handleActionsApprove))
	mux.HandleFunc("POST /v1/actions/{id}/rollback", s.requireCapability(CapRemediateApprove, s.handleActionsRollback))

	mux.HandleFunc("POST /v1/nl/query", s.requireCapability(CapTelemetryRead, s.handleNLQuery))

	// MCP: authenticated at the transport level; each tool call is then
	// re-checked against its own MinRole inside handleMCP (FR-F12-6).
	mux.HandleFunc("POST /v1/mcp", s.authenticate(s.handleMCP))

	s.mountStatic(mux)
	return mux
}

// mountStatic serves web.FS() at "/". A path with no matching embedded file
// (a client-side SPA route like /traces) falls back to index.html so the
// browser-side router in web/static/js/app.js takes over — the same
// pattern every history-API SPA without a framework uses.
func (s *HTTPServer) mountStatic(mux *http.ServeMux) {
	assets := web.FS()
	fileServer := http.FileServer(http.FS(assets))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}
		if f, err := assets.Open(trimLeadingSlash(path)); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: unknown path, serve index.html so the client router
		// resolves it (FR-F12-2's History API routing).
		r2 := new(http.Request)
		*r2 = *r
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}

// Start implements Server.Start (DR-30's CC-24 rename target). It binds
// addr and serves until ctx is cancelled or Shutdown is called.
func (s *HTTPServer) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.srv = &http.Server{Handler: s.mux}
	srv := s.srv
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		return s.Shutdown(context.Background())
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *HTTPServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

func (s *HTTPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Addr().String()
	}
	return s.addr
}

// Routes exposes the mux directly for httptest.NewServer/http.Handler use
// in tests (F12 §4.3's `Server` interface: "Routes() *http.ServeMux // for
// testability via httptest").
func (s *HTTPServer) Routes() *http.ServeMux { return s.mux }
