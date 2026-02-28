package api

import (
    "crypto/subtle"
    "net/http"
    "strings"

    "task.hh/internal/store"
)

type Server struct {
    store     *store.Store
    authToken string
    logger    Logger
}

type Logger interface {
    Printf(format string, v ...any)
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

func NewServer(st *store.Store, authToken string, logger Logger) *Server {
    if logger == nil {
        logger = nopLogger{}
    }
    return &Server{
        store:     st,
        authToken: authToken,
        logger:    logger,
    }
}

func (s *Server) Routes() http.Handler {
    mux := http.NewServeMux()
    mux.Handle("/v1/users", s.authMiddleware(http.HandlerFunc(s.handleUsers)))
    mux.Handle("/v1/withdrawals", s.authMiddleware(http.HandlerFunc(s.handleWithdrawals)))
    mux.Handle("/v1/withdrawals/", s.authMiddleware(http.HandlerFunc(s.handleWithdrawalByID)))
    return mux
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := extractBearerToken(r.Header.Get("Authorization"))
        if !secureCompare(token, s.authToken) {
            writeError(w, http.StatusUnauthorized, "unauthorized")
            return
        }
        next.ServeHTTP(w, r)
    })
}

func extractBearerToken(header string) string {
    if header == "" {
        return ""
    }
    parts := strings.SplitN(header, " ", 2)
    if len(parts) != 2 {
        return ""
    }
    if !strings.EqualFold(parts[0], "Bearer") {
        return ""
    }
    return strings.TrimSpace(parts[1])
}

func secureCompare(a, b string) bool {
    if len(a) != len(b) {
        return false
    }
    return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
