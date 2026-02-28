package store

import "time"

const (
    StatusPending   = "pending"
    StatusConfirmed = "confirmed"
)

const DirectionDebit = "debit"

type Withdrawal struct {
    ID             int64
    UserID         int64
    Amount         int64
    Currency       string
    Destination    string
    Status         string
    IdempotencyKey string
    CreatedAt      time.Time
}

type CreateWithdrawalInput struct {
    UserID         int64
    Amount         int64
    Currency       string
    Destination    string
    IdempotencyKey string
}

type User struct {
    ID        int64
    Balance   int64
    CreatedAt time.Time
}

type LedgerEntry struct {
    ID           int64
    UserID       int64
    WithdrawalID int64
    Amount       int64
    Currency     string
    Direction    string
    CreatedAt    time.Time
}
