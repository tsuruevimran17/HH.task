package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "task.hh/internal/api"
    "task.hh/internal/store"
)

type config struct {
    DatabaseURL string
    AuthToken   string
    Port        string
}

func loadConfig() (config, error) {
    dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
    if dbURL == "" {
        host := strings.TrimSpace(os.Getenv("DB_HOST"))
        if host == "" {
            host = "localhost"
        }
        port := strings.TrimSpace(os.Getenv("DB_PORT"))
        if port == "" {
            port = "5432"
        }
        user := strings.TrimSpace(os.Getenv("DB_USER"))
        password := strings.TrimSpace(os.Getenv("DB_PASSWORD"))
        name := strings.TrimSpace(os.Getenv("DB_NAME"))
        sslmode := strings.TrimSpace(os.Getenv("DB_SSLMODE"))
        if sslmode == "" {
            sslmode = "disable"
        }
        if user == "" || password == "" || name == "" {
            return config{}, errors.New("DATABASE_URL or DB_USER/DB_PASSWORD/DB_NAME are required")
        }
        dbURL = fmt.Sprintf(
            "host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
            host,
            port,
            user,
            password,
            name,
            sslmode,
        )
    }

    authToken := strings.TrimSpace(os.Getenv("AUTH_TOKEN"))
    if authToken == "" {
        return config{}, errors.New("AUTH_TOKEN is required")
    }

    port := strings.TrimSpace(os.Getenv("PORT"))
    if port == "" {
        port = "8080"
    }

    return config{
        DatabaseURL: dbURL,
        AuthToken:   authToken,
        Port:        port,
    }, nil
}

func main() {
    cfg, err := loadConfig()
    if err != nil {
        log.Fatalf("config error: %v", err)
    }

    ctx := context.Background()
    pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
    if err != nil {
        log.Fatalf("db error: %v", err)
    }
    defer pool.Close()

    logger := log.New(os.Stdout, "", log.LstdFlags)
    srv := api.NewServer(store.New(pool), cfg.AuthToken, logger)

    httpServer := &http.Server{
        Addr:              ":" + cfg.Port,
        Handler:           srv.Routes(),
        ReadHeaderTimeout: 5 * time.Second,
    }

    go func() {
        logger.Printf("listening on %s", httpServer.Addr)
        if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
            logger.Fatalf("server error: %v", err)
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = httpServer.Shutdown(ctxShutdown)
}
