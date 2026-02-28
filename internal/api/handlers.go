package api

import (
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "strconv"
    "strings"
    "time"

    "task.hh/internal/store"
)

type createWithdrawalRequest struct {
    UserID         int64  `json:"user_id"`
    Amount         int64  `json:"amount"`
    Currency       string `json:"currency"`
    Destination    string `json:"destination"`
    IdempotencyKey string `json:"idempotency_key"`
}

type createUserRequest struct {
    ID      int64 `json:"id"`
    Balance int64 `json:"balance"`
}

type withdrawalResponse struct {
    ID             int64     `json:"id"`
    UserID         int64     `json:"user_id"`
    Amount         int64     `json:"amount"`
    Currency       string    `json:"currency"`
    Destination    string    `json:"destination"`
    Status         string    `json:"status"`
    IdempotencyKey string    `json:"idempotency_key"`
    CreatedAt      time.Time `json:"created_at"`
}

type userResponse struct {
    ID        int64     `json:"id"`
    Balance   int64     `json:"balance"`
    CreatedAt time.Time `json:"created_at"`
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        s.handleCreateUser(w, r)
        return
    }
    writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (s *Server) handleWithdrawals(w http.ResponseWriter, r *http.Request) {
    if r.Method == http.MethodPost {
        s.handleCreateWithdrawal(w, r)
        return
    }

    writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func (s *Server) handleWithdrawalByID(w http.ResponseWriter, r *http.Request) {
    path := strings.TrimPrefix(r.URL.Path, "/v1/withdrawals/")
    if path == "" {
        writeError(w, http.StatusNotFound, "not_found")
        return
    }
    parts := strings.Split(path, "/")
    if len(parts) == 2 && parts[1] == "confirm" {
        id, err := strconv.ParseInt(parts[0], 10, 64)
        if err != nil || id <= 0 {
            writeError(w, http.StatusBadRequest, "invalid_id")
            return
        }
        s.handleConfirmWithdrawal(w, r, id)
        return
    }
    if len(parts) != 1 {
        writeError(w, http.StatusNotFound, "not_found")
        return
    }
    if r.Method != http.MethodGet {
        writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
        return
    }

    id, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil || id <= 0 {
        writeError(w, http.StatusBadRequest, "invalid_id")
        return
    }

    withdrawal, err := s.store.GetWithdrawal(r.Context(), id)
    if err != nil {
        if errors.Is(err, store.ErrNotFound) {
            writeError(w, http.StatusNotFound, "not_found")
            return
        }
        s.logger.Printf("get withdrawal error: %v", err)
        writeError(w, http.StatusInternalServerError, "internal_error")
        return
    }

    writeJSON(w, http.StatusOK, toWithdrawalResponse(withdrawal))
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
    var req createUserRequest

    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()
    if err := dec.Decode(&req); err != nil {
        s.logEvent("user_create_failed", map[string]any{
            "reason": "invalid_request",
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }
    if err := dec.Decode(&struct{}{}); err != io.EOF {
        s.logEvent("user_create_failed", map[string]any{
            "reason": "invalid_request",
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }

    if err := validateCreateUser(req); err != nil {
        s.logEvent("user_create_failed", map[string]any{
            "reason":  "invalid_request",
            "user_id": req.ID,
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }

    user, err := s.store.CreateUser(r.Context(), req.ID, req.Balance)
    if err != nil {
        reason := "internal_error"
        switch {
        case errors.Is(err, store.ErrUserExists):
            reason = "user_exists"
            writeError(w, http.StatusConflict, "user_exists")
        default:
            s.logger.Printf("create user error: %v", err)
            writeError(w, http.StatusInternalServerError, "internal_error")
        }
        s.logEvent("user_create_failed", map[string]any{
            "reason":  reason,
            "user_id": req.ID,
            "balance": req.Balance,
        })
        return
    }

    s.logEvent("user_created", map[string]any{
        "user_id": user.ID,
        "balance": user.Balance,
    })
    writeJSON(w, http.StatusCreated, toUserResponse(user))
}

func (s *Server) handleCreateWithdrawal(w http.ResponseWriter, r *http.Request) {
    var req createWithdrawalRequest

    dec := json.NewDecoder(r.Body)
    dec.DisallowUnknownFields()
    if err := dec.Decode(&req); err != nil {
        s.logEvent("withdrawal_create_failed", map[string]any{
            "reason": "invalid_request",
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }
    if err := dec.Decode(&struct{}{}); err != io.EOF {
        s.logEvent("withdrawal_create_failed", map[string]any{
            "reason": "invalid_request",
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }

    if err := validateCreateWithdrawal(req); err != nil {
        s.logEvent("withdrawal_create_failed", map[string]any{
            "reason":  "invalid_request",
            "user_id": req.UserID,
        })
        writeError(w, http.StatusBadRequest, "invalid_request")
        return
    }

    input := store.CreateWithdrawalInput{
        UserID:         req.UserID,
        Amount:         req.Amount,
        Currency:       strings.TrimSpace(req.Currency),
        Destination:    strings.TrimSpace(req.Destination),
        IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
    }

    withdrawal, err := s.store.CreateWithdrawal(r.Context(), input)
    if err != nil {
        reason := "internal_error"
        switch {
        case errors.Is(err, store.ErrInsufficientBalance):
            reason = "insufficient_balance"
            writeError(w, http.StatusConflict, "insufficient_balance")
        case errors.Is(err, store.ErrIdempotencyConflict):
            reason = "idempotency_conflict"
            writeError(w, http.StatusUnprocessableEntity, "idempotency_conflict")
        case errors.Is(err, store.ErrUserNotFound):
            reason = "user_not_found"
            writeError(w, http.StatusNotFound, "user_not_found")
        default:
            s.logger.Printf("create withdrawal error: %v", err)
            writeError(w, http.StatusInternalServerError, "internal_error")
        }
        s.logEvent("withdrawal_create_failed", map[string]any{
            "reason":  reason,
            "user_id": input.UserID,
            "amount":  input.Amount,
            "currency": input.Currency,
        })
        return
    }

    s.logEvent("withdrawal_created", map[string]any{
        "withdrawal_id": withdrawal.ID,
        "user_id":       withdrawal.UserID,
        "amount":        withdrawal.Amount,
        "currency":      withdrawal.Currency,
        "status":        withdrawal.Status,
    })
    writeJSON(w, http.StatusCreated, toWithdrawalResponse(withdrawal))
}

func (s *Server) handleConfirmWithdrawal(w http.ResponseWriter, r *http.Request, id int64) {
    if r.Method != http.MethodPost {
        writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
        return
    }

    withdrawal, err := s.store.ConfirmWithdrawal(r.Context(), id)
    if err != nil {
        reason := "internal_error"
        switch {
        case errors.Is(err, store.ErrNotFound):
            reason = "not_found"
            writeError(w, http.StatusNotFound, "not_found")
        case errors.Is(err, store.ErrInvalidStatus):
            reason = "invalid_status"
            writeError(w, http.StatusConflict, "invalid_status")
        default:
            s.logger.Printf("confirm withdrawal error: %v", err)
            writeError(w, http.StatusInternalServerError, "internal_error")
        }
        s.logEvent("withdrawal_confirm_failed", map[string]any{
            "withdrawal_id": id,
            "reason":        reason,
        })
        return
    }

    s.logEvent("withdrawal_confirmed", map[string]any{
        "withdrawal_id": withdrawal.ID,
        "user_id":       withdrawal.UserID,
        "status":        withdrawal.Status,
    })
    writeJSON(w, http.StatusOK, toWithdrawalResponse(withdrawal))
}

func validateCreateWithdrawal(req createWithdrawalRequest) error {
    if req.UserID <= 0 {
        return errors.New("invalid user_id")
    }
    if req.Amount <= 0 {
        return errors.New("invalid amount")
    }
    if strings.TrimSpace(req.Currency) != "USDT" {
        return errors.New("invalid currency")
    }
    if strings.TrimSpace(req.Destination) == "" {
        return errors.New("invalid destination")
    }
    if strings.TrimSpace(req.IdempotencyKey) == "" {
        return errors.New("invalid idempotency_key")
    }
    return nil
}

func validateCreateUser(req createUserRequest) error {
    if req.ID <= 0 {
        return errors.New("invalid id")
    }
    if req.Balance < 0 {
        return errors.New("invalid balance")
    }
    return nil
}

func toWithdrawalResponse(w store.Withdrawal) withdrawalResponse {
    return withdrawalResponse{
        ID:             w.ID,
        UserID:         w.UserID,
        Amount:         w.Amount,
        Currency:       w.Currency,
        Destination:    w.Destination,
        Status:         w.Status,
        IdempotencyKey: w.IdempotencyKey,
        CreatedAt:      w.CreatedAt,
    }
}

func toUserResponse(u store.User) userResponse {
    return userResponse{
        ID:        u.ID,
        Balance:   u.Balance,
        CreatedAt: u.CreatedAt,
    }
}
