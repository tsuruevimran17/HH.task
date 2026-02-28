package api_test

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"

    "task.hh/internal/api"
    "task.hh/internal/store"
)

type testEnv struct {
    pool      *pgxpool.Pool
    server    *httptest.Server
    client    *http.Client
    authToken string
}

type withdrawalResponse struct {
    ID             int64  `json:"id"`
    UserID         int64  `json:"user_id"`
    Amount         int64  `json:"amount"`
    Currency       string `json:"currency"`
    Destination    string `json:"destination"`
    Status         string `json:"status"`
    IdempotencyKey string `json:"idempotency_key"`
}

func setupTest(t *testing.T) *testEnv {
    t.Helper()

    dbURL := os.Getenv("DATABASE_URL")
    if dbURL == "" {
        t.Skip("DATABASE_URL is not set")
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    pool, err := pgxpool.New(ctx, dbURL)
    if err != nil {
        t.Fatalf("db connection: %v", err)
    }
    defer pool.Close()

    applySchema(t, pool)
    resetDB(t, pool)

    authToken := "test-token"
    srv := api.NewServer(store.New(pool), authToken, log.New(io.Discard, "", 0))
    ts := httptest.NewServer(srv.Routes())

    return &testEnv{
        pool:      pool,
        server:    ts,
        client:    &http.Client{Timeout: 3 * time.Second},
        authToken: authToken,
    }
}

func (e *testEnv) close() {
    e.server.Close()
    e.pool.Close()
}

func (e *testEnv) doRequest(t *testing.T, method, path, body string) *http.Response {
    t.Helper()

    req, err := http.NewRequest(method, e.server.URL+path, strings.NewReader(body))
    if err != nil {
        t.Fatalf("new request: %v", err)
    }
    req.Header.Set("Authorization", "Bearer "+e.authToken)
    req.Header.Set("Content-Type", "application/json")

    resp, err := e.client.Do(req)
    if err != nil {
        t.Fatalf("do request: %v", err)
    }
    return resp
}

func TestCreateWithdrawalSuccess(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":200,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp.StatusCode)
    }

    var got withdrawalResponse
    if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
        t.Fatalf("decode response: %v", err)
    }

    if got.Status != store.StatusPending {
        t.Fatalf("expected status %s, got %s", store.StatusPending, got.Status)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 800 {
        t.Fatalf("expected balance 800, got %d", balance)
    }

    count, sum := getLedgerSummary(t, env.pool, 1)
    if count != 1 || sum != 200 {
        t.Fatalf("expected ledger count 1 and sum 200, got %d and %d", count, sum)
    }
}

func TestCreateWithdrawalInsufficientBalance(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 100)

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":200,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusConflict {
        t.Fatalf("expected %d, got %d", http.StatusConflict, resp.StatusCode)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 100 {
        t.Fatalf("expected balance 100, got %d", balance)
    }

    count := getWithdrawalCount(t, env.pool, 1)
    if count != 0 {
        t.Fatalf("expected 0 withdrawals, got %d", count)
    }

    ledgerCount, _ := getLedgerSummary(t, env.pool, 1)
    if ledgerCount != 0 {
        t.Fatalf("expected 0 ledger entries, got %d", ledgerCount)
    }
}

func TestCreateWithdrawalIdempotency(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    body := `{"user_id":1,"amount":100,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`

    resp1 := env.doRequest(t, http.MethodPost, "/v1/withdrawals", body)
    defer resp1.Body.Close()

    if resp1.StatusCode != http.StatusCreated {
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp1.StatusCode)
    }

    var first withdrawalResponse
    if err := json.NewDecoder(resp1.Body).Decode(&first); err != nil {
        t.Fatalf("decode response: %v", err)
    }

    resp2 := env.doRequest(t, http.MethodPost, "/v1/withdrawals", body)
    defer resp2.Body.Close()

    if resp2.StatusCode != http.StatusCreated {
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp2.StatusCode)
    }

    var second withdrawalResponse
    if err := json.NewDecoder(resp2.Body).Decode(&second); err != nil {
        t.Fatalf("decode response: %v", err)
    }

    if first.ID != second.ID {
        t.Fatalf("expected same withdrawal id, got %d and %d", first.ID, second.ID)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 900 {
        t.Fatalf("expected balance 900, got %d", balance)
    }

    count := getWithdrawalCount(t, env.pool, 1)
    if count != 1 {
        t.Fatalf("expected 1 withdrawal, got %d", count)
    }

    ledgerCount, sum := getLedgerSummary(t, env.pool, 1)
    if ledgerCount != 1 || sum != 100 {
        t.Fatalf("expected ledger count 1 and sum 100, got %d and %d", ledgerCount, sum)
    }
}

func TestCreateWithdrawalIdempotencyConflict(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    resp1 := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":100,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    resp1.Body.Close()

    resp2 := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":200,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    defer resp2.Body.Close()

    if resp2.StatusCode != http.StatusUnprocessableEntity {
        t.Fatalf("expected %d, got %d", http.StatusUnprocessableEntity, resp2.StatusCode)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 900 {
        t.Fatalf("expected balance 900, got %d", balance)
    }

    count := getWithdrawalCount(t, env.pool, 1)
    if count != 1 {
        t.Fatalf("expected 1 withdrawal, got %d", count)
    }
}

func TestCreateWithdrawalInvalidAmount(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":0,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusBadRequest {
        t.Fatalf("expected %d, got %d", http.StatusBadRequest, resp.StatusCode)
    }

    count := getWithdrawalCount(t, env.pool, 1)
    if count != 0 {
        t.Fatalf("expected 0 withdrawals, got %d", count)
    }
}

func TestConcurrentWithdrawals(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 100)

    type result struct {
        status int
        err    error
    }

    var wg sync.WaitGroup
    results := make(chan result, 2)

    for i := 0; i < 2; i++ {
        wg.Add(1)
        go func(i int) {
            defer wg.Done()
            body := fmt.Sprintf(`{"user_id":1,"amount":80,"currency":"USDT","destination":"addr","idempotency_key":"k%d"}`, i+1)
            req, err := http.NewRequest(http.MethodPost, env.server.URL+"/v1/withdrawals", strings.NewReader(body))
            if err != nil {
                results <- result{err: err}
                return
            }
            req.Header.Set("Authorization", "Bearer "+env.authToken)
            req.Header.Set("Content-Type", "application/json")

            resp, err := env.client.Do(req)
            if err != nil {
                results <- result{err: err}
                return
            }
            resp.Body.Close()
            results <- result{status: resp.StatusCode}
        }(i)
    }

    wg.Wait()
    close(results)

    created := 0
    conflicts := 0

    for res := range results {
        if res.err != nil {
            t.Fatalf("request error: %v", res.err)
        }
        switch res.status {
        case http.StatusCreated:
            created++
        case http.StatusConflict:
            conflicts++
        default:
            t.Fatalf("unexpected status: %d", res.status)
        }
    }

    if created != 1 || conflicts != 1 {
        t.Fatalf("expected 1 created and 1 conflict, got %d and %d", created, conflicts)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 20 {
        t.Fatalf("expected balance 20, got %d", balance)
    }
}

func TestConfirmWithdrawalSuccess(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":100,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    if resp.StatusCode != http.StatusCreated {
        resp.Body.Close()
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp.StatusCode)
    }

    var created withdrawalResponse
    if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
        resp.Body.Close()
        t.Fatalf("decode response: %v", err)
    }
    resp.Body.Close()

    confirm := env.doRequest(t, http.MethodPost, fmt.Sprintf("/v1/withdrawals/%d/confirm", created.ID), "")
    defer confirm.Body.Close()

    if confirm.StatusCode != http.StatusOK {
        t.Fatalf("expected %d, got %d", http.StatusOK, confirm.StatusCode)
    }

    var confirmed withdrawalResponse
    if err := json.NewDecoder(confirm.Body).Decode(&confirmed); err != nil {
        t.Fatalf("decode response: %v", err)
    }

    if confirmed.Status != store.StatusConfirmed {
        t.Fatalf("expected status %s, got %s", store.StatusConfirmed, confirmed.Status)
    }
}

func TestConfirmWithdrawalIdempotent(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 1000)

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals", `{"user_id":1,"amount":100,"currency":"USDT","destination":"addr","idempotency_key":"k1"}`)
    if resp.StatusCode != http.StatusCreated {
        resp.Body.Close()
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp.StatusCode)
    }
    var created withdrawalResponse
    if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
        resp.Body.Close()
        t.Fatalf("decode response: %v", err)
    }
    resp.Body.Close()

    confirm1 := env.doRequest(t, http.MethodPost, fmt.Sprintf("/v1/withdrawals/%d/confirm", created.ID), "")
    confirm1.Body.Close()

    confirm2 := env.doRequest(t, http.MethodPost, fmt.Sprintf("/v1/withdrawals/%d/confirm", created.ID), "")
    defer confirm2.Body.Close()

    if confirm2.StatusCode != http.StatusOK {
        t.Fatalf("expected %d, got %d", http.StatusOK, confirm2.StatusCode)
    }

    var confirmed withdrawalResponse
    if err := json.NewDecoder(confirm2.Body).Decode(&confirmed); err != nil {
        t.Fatalf("decode response: %v", err)
    }
    if confirmed.ID != created.ID || confirmed.Status != store.StatusConfirmed {
        t.Fatalf("expected confirmed withdrawal %d, got %d with status %s", created.ID, confirmed.ID, confirmed.Status)
    }
}

func TestConfirmWithdrawalNotFound(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    resp := env.doRequest(t, http.MethodPost, "/v1/withdrawals/999/confirm", "")
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusNotFound {
        t.Fatalf("expected %d, got %d", http.StatusNotFound, resp.StatusCode)
    }
}

func seedUser(t *testing.T, pool *pgxpool.Pool, id int64, balance int64) {
    t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    _, err := pool.Exec(ctx, "INSERT INTO users (id, balance) VALUES ($1, $2)", id, balance)
    if err != nil {
        t.Fatalf("seed user: %v", err)
    }
}

func getBalance(t *testing.T, pool *pgxpool.Pool, id int64) int64 {
    t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    var balance int64
    err := pool.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1", id).Scan(&balance)
    if err != nil {
        t.Fatalf("get balance: %v", err)
    }
    return balance
}

func getWithdrawalCount(t *testing.T, pool *pgxpool.Pool, userID int64) int {
    t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    var count int
    err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM withdrawals WHERE user_id = $1", userID).Scan(&count)
    if err != nil {
        t.Fatalf("get withdrawal count: %v", err)
    }
    return count
}

func getLedgerSummary(t *testing.T, pool *pgxpool.Pool, userID int64) (int, int64) {
    t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    var count int
    var sum int64
    err := pool.QueryRow(ctx, "SELECT COUNT(*), COALESCE(SUM(amount), 0) FROM ledger_entries WHERE user_id = $1", userID).Scan(&count, &sum)
    if err != nil {
        t.Fatalf("get ledger summary: %v", err)
    }
    return count, sum
}

func applySchema(t *testing.T, pool *pgxpool.Pool) {
    t.Helper()

    schema := loadSchema(t)
    statements := strings.Split(schema, ";")

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    for _, stmt := range statements {
        s := strings.TrimSpace(stmt)
        if s == "" {
            continue
        }
        if _, err := pool.Exec(ctx, s); err != nil {
            t.Fatalf("apply schema: %v", err)
        }
    }
}

func resetDB(t *testing.T, pool *pgxpool.Pool) {
    t.Helper()

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if _, err := pool.Exec(ctx, "TRUNCATE ledger_entries, withdrawals, users RESTART IDENTITY"); err != nil {
        t.Fatalf("reset db: %v", err)
    }
}

func loadSchema(t *testing.T) string {
    t.Helper()

    wd, err := os.Getwd()
    if err != nil {
        t.Fatalf("getwd: %v", err)
    }

    dir := wd
    for i := 0; i < 6; i++ {
        path := filepath.Join(dir, "schema.sql")
        if _, err := os.Stat(path); err == nil {
            data, err := os.ReadFile(path)
            if err != nil {
                t.Fatalf("read schema: %v", err)
            }
            return string(data)
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            break
        }
        dir = parent
    }

    t.Fatalf("schema.sql not found from %s", wd)
    return ""
}
