package store

import "errors"

var (
    ErrInsufficientBalance = errors.New("insufficient balance")
    ErrIdempotencyConflict = errors.New("idempotency conflict")
    ErrNotFound            = errors.New("not found")
    ErrUserNotFound        = errors.New("user not found")
    ErrUserExists          = errors.New("user exists")
    ErrInvalidStatus       = errors.New("invalid status")
)
