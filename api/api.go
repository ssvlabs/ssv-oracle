package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ssvlabs/ssv-oracle/logger"
	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
)

const (
	defaultReadTimeout  = 10 * time.Second
	defaultWriteTimeout = 10 * time.Second
	defaultIdleTimeout  = 60 * time.Second
)

//go:embed ui/index.html
var uiFS embed.FS

type apiStorage interface {
	GetLatestCommit(ctx context.Context) (*storage.OracleCommit, error)
	GetCommitByEpoch(ctx context.Context, epoch uint64) (*storage.OracleCommit, *uint64, *uint64, error)
	GetAllClusterInfo(ctx context.Context) (storage.AllClusterInfo, error)
}

// Server is the HTTP API server.
type Server struct {
	storage apiStorage
	addr    string
	server  *http.Server
}

// New creates a new API server.
func New(storage apiStorage, addr string) *Server {
	return &Server{
		storage: storage,
		addr:    addr,
	}
}

// Run starts the API server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/v1/commit", s.handleGetCommit)
	apiMux.HandleFunc("GET /api/v1/proof/{clusterId}", s.handleGetProof)

	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/", s.contentTypeMiddleware(apiMux))
	mux.Handle("GET /metrics", promhttp.HandlerFor(
		prometheus.DefaultGatherer,
		promhttp.HandlerOpts{EnableOpenMetrics: true},
	))
	mux.HandleFunc("GET /", s.handleUI)

	handler := s.recoveryMiddleware(mux)

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      handler,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
		IdleTimeout:  defaultIdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Infow("API server starting", "addr", s.addr)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		logger.Info("API server shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (s *Server) contentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Errorw("Panic recovered", "error", err, "path", r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(ErrorResponse{Error: internalError})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Errorw("Failed to encode response", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, ErrorResponse{Error: message})
}

func buildTree(balances []storage.ClusterBalance) *merkle.Tree {
	clusterMap := make(map[[32]byte]uint32)
	for _, bal := range balances {
		var clusterID [32]byte
		copy(clusterID[:], bal.ClusterID)
		clusterMap[clusterID] = bal.EffectiveBalance
	}
	return merkle.NewTree(clusterMap)
}
