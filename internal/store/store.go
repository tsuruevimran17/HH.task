package store

import (
    "context"
    "errors"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgconn"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
    pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
    return &Store{pool: pool}
}

func (s *Store) CreateUser(ctx context.Context, id int64, balance int64) (User, error) {
    var u User
    err := s.pool.QueryRow(ctx, `
        INSERT INTO users (id, balance)
        VALUES ($1, $2)
        RETURNING id, balance, created_at
    `, id, balance).Scan(
        &u.ID,
        &u.Balance,
        &u.CreatedAt,
    )
    if err != nil {
        if isUniqueViolation(err) {
            return User{}, ErrUserExists
        }
        return User{}, err
    }
    return u, nil
}

func (s *Store) CreateWithdrawal(ctx context.Context, input CreateWithdrawalInput) (Withdrawal, error) {
    tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return Withdrawal{}, err
    }
    defer func() {
        _ = tx.Rollback(ctx)
    }()

    var balance int64
    err = tx.QueryRow(ctx, "SELECT balance FROM users WHERE id = $1 FOR UPDATE", input.UserID).Scan(&balance)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return Withdrawal{}, ErrUserNotFound
        }
        return Withdrawal{}, err
    }

    existing, err := getWithdrawalByIdempotency(ctx, tx, input.UserID, input.IdempotencyKey)
    if err == nil {
        if !samePayload(existing, input) {
            return Withdrawal{}, ErrIdempotencyConflict
        }
        return existing, nil
    }
    if !errors.Is(err, pgx.ErrNoRows) {
        return Withdrawal{}, err
    }

    if balance < input.Amount {
        return Withdrawal{}, ErrInsufficientBalance
    }

    created, err := insertWithdrawal(ctx, tx, input)
    if err != nil {
        if isUniqueViolation(err) {
            existing, gerr := getWithdrawalByIdempotency(ctx, tx, input.UserID, input.IdempotencyKey)
            if gerr == nil {
                if !samePayload(existing, input) {
                    return Withdrawal{}, ErrIdempotencyConflict
                }
                return existing, nil
            }
        }
        return Withdrawal{}, err
    }

    _, err = tx.Exec(ctx, "UPDATE users SET balance = balance - $1 WHERE id = $2", input.Amount, input.UserID)
    if err != nil {
        return Withdrawal{}, err
    }

    if err := insertLedgerEntry(ctx, tx, created.ID, input); err != nil {
        return Withdrawal{}, err
    }

    if err := tx.Commit(ctx); err != nil {
        return Withdrawal{}, err
    }

    return created, nil
}

func (s *Store) GetWithdrawal(ctx context.Context, id int64) (Withdrawal, error) {
    var w Withdrawal
    err := s.pool.QueryRow(ctx, `
        SELECT id, user_id, amount, currency, destination, status, idempotency_key, created_at
        FROM withdrawals
        WHERE id = $1
    `, id).Scan(
        &w.ID,
        &w.UserID,
        &w.Amount,
        &w.Currency,
        &w.Destination,
        &w.Status,
        &w.IdempotencyKey,
        &w.CreatedAt,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return Withdrawal{}, ErrNotFound
        }
        return Withdrawal{}, err
    }
    return w, nil
}

func (s *Store) ConfirmWithdrawal(ctx context.Context, id int64) (Withdrawal, error) {
    tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return Withdrawal{}, err
    }
    defer func() {
        _ = tx.Rollback(ctx)
    }()

    var w Withdrawal
    err = tx.QueryRow(ctx, `
        SELECT id, user_id, amount, currency, destination, status, idempotency_key, created_at
        FROM withdrawals
        WHERE id = $1
        FOR UPDATE
    `, id).Scan(
        &w.ID,
        &w.UserID,
        &w.Amount,
        &w.Currency,
        &w.Destination,
        &w.Status,
        &w.IdempotencyKey,
        &w.CreatedAt,
    )
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return Withdrawal{}, ErrNotFound
        }
        return Withdrawal{}, err
    }

    if w.Status == StatusConfirmed {
        if err := tx.Commit(ctx); err != nil {
            return Withdrawal{}, err
        }
        return w, nil
    }

    if w.Status != StatusPending {
        return Withdrawal{}, ErrInvalidStatus
    }

    _, err = tx.Exec(ctx, "UPDATE withdrawals SET status = $1 WHERE id = $2", StatusConfirmed, id)
    if err != nil {
        return Withdrawal{}, err
    }
    w.Status = StatusConfirmed

    if err := tx.Commit(ctx); err != nil {
        return Withdrawal{}, err
    }

    return w, nil
}

func insertWithdrawal(ctx context.Context, tx pgx.Tx, input CreateWithdrawalInput) (Withdrawal, error) {
    var w Withdrawal
    err := tx.QueryRow(ctx, `
        INSERT INTO withdrawals (user_id, amount, currency, destination, status, idempotency_key)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, user_id, amount, currency, destination, status, idempotency_key, created_at
    `,
        input.UserID,
        input.Amount,
        input.Currency,
        input.Destination,
        StatusPending,
        input.IdempotencyKey,
    ).Scan(
        &w.ID,
        &w.UserID,
        &w.Amount,
        &w.Currency,
        &w.Destination,
        &w.Status,
        &w.IdempotencyKey,
        &w.CreatedAt,
    )
    return w, err
}

func insertLedgerEntry(ctx context.Context, tx pgx.Tx, withdrawalID int64, input CreateWithdrawalInput) error {
    _, err := tx.Exec(ctx, `
        INSERT INTO ledger_entries (user_id, withdrawal_id, amount, currency, direction)
        VALUES ($1, $2, $3, $4, $5)
    `, input.UserID, withdrawalID, input.Amount, input.Currency, DirectionDebit)
    return err
}

func getWithdrawalByIdempotency(ctx context.Context, tx pgx.Tx, userID int64, key string) (Withdrawal, error) {
    var w Withdrawal
    err := tx.QueryRow(ctx, `
        SELECT id, user_id, amount, currency, destination, status, idempotency_key, created_at
        FROM withdrawals
        WHERE user_id = $1 AND idempotency_key = $2
    `, userID, key).Scan(
        &w.ID,
        &w.UserID,
        &w.Amount,
        &w.Currency,
        &w.Destination,
        &w.Status,
        &w.IdempotencyKey,
        &w.CreatedAt,
    )
    return w, err
}

func samePayload(w Withdrawal, input CreateWithdrawalInput) bool {
    return w.Amount == input.Amount && w.Currency == input.Currency && w.Destination == input.Destination
}

func isUniqueViolation(err error) bool {
    pgErr, ok := err.(*pgconn.PgError)
    if !ok {
        return false
    }
    return pgErr.Code == "23505"
}
