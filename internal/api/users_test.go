package api_test

import (
    "encoding/json"
    "net/http"
    "testing"
)

type userResponse struct {
    ID      int64 `json:"id"`
    Balance int64 `json:"balance"`
}

func TestCreateUserSuccess(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    resp := env.doRequest(t, http.MethodPost, "/v1/users", `{"id":1,"balance":1000}`)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusCreated {
        t.Fatalf("expected %d, got %d", http.StatusCreated, resp.StatusCode)
    }

    var got userResponse
    if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
        t.Fatalf("decode response: %v", err)
    }

    if got.ID != 1 || got.Balance != 1000 {
        t.Fatalf("unexpected response: id=%d balance=%d", got.ID, got.Balance)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 1000 {
        t.Fatalf("expected balance 1000, got %d", balance)
    }
}

func TestCreateUserConflict(t *testing.T) {
    env := setupTest(t)
    defer env.close()

    seedUser(t, env.pool, 1, 100)

    resp := env.doRequest(t, http.MethodPost, "/v1/users", `{"id":1,"balance":200}`)
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusConflict {
        t.Fatalf("expected %d, got %d", http.StatusConflict, resp.StatusCode)
    }

    balance := getBalance(t, env.pool, 1)
    if balance != 100 {
        t.Fatalf("expected balance 100, got %d", balance)
    }
}
