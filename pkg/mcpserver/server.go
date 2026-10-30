package mcpserver

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
)

type contextKey int

const agentContextKey contextKey = iota

// Server is a standalone MCP endpoint backed by a picoclaw workspace. It
// shares files with the gateway but never joins its process.
type Server struct {
	workspace string
	cfg       Config
	mcp       *mcp.Server
}

// New builds a server for the given workspace and config.
func New(workspace string, cfg Config) *Server {
	s := &Server{workspace: workspace, cfg: cfg}
	s.mcp = mcp.NewServer(&mcp.Implementation{
		Name:    "picoclaw-memory",
		Version: config.Version,
	}, nil)
	s.registerTools()
	return s
}

func agentFrom(ctx context.Context) (Agent, bool) {
	a, ok := ctx.Value(agentContextKey).(Agent)
	return a, ok
}

// Handler returns the HTTP handler: /mcp (authenticated) and /healthz.
func (s *Server) Handler() http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.mcp
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", s.authenticate(streamable))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(s.cfg.Agents) == 0 {
			http.Error(w, "mcp: no agents configured in mcp/config.json", http.StatusServiceUnavailable)
			return
		}
		header := r.Header.Get("Authorization")
		token, _ := strings.CutPrefix(header, "Bearer")
		agent, ok := s.cfg.AgentByToken(strings.TrimSpace(token))
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), agentContextKey, agent)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Addr returns the listen address.
func (s *Server) Addr() string {
	return net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
}

// ListenAndServe runs until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.Addr(),
		Handler:           s.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}
