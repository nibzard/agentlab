package db

import (
	"context"
	"database/sql"
	"errors"
)

type Operation struct {
	ID         string `json:"id"`
	Caller     string `json:"-"`
	Key        string `json:"-"`
	Payload    string `json:"-"`
	State      string `json:"state"`
	VMID       *int   `json:"vmid,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Result     string `json:"-"`
}

type operationContextKey struct{}

func WithOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationContextKey{}, id)
}

// BeginOperation atomically reserves a caller-scoped key before any allocation.
func (s *Store) BeginOperation(ctx context.Context, id, caller, key, payload string) (Operation, bool, error) {
	result, err := s.DB.ExecContext(ctx, `INSERT INTO operations (id,caller,request_key,payload,state) VALUES (?,?,?,?,'accepted') ON CONFLICT(caller,request_key) DO NOTHING`, id, caller, key, payload)
	if err != nil {
		return Operation{}, false, err
	}
	n, _ := result.RowsAffected()
	op, err := s.operationQuery(ctx, `SELECT id,caller,request_key,payload,state,vmid,http_status,result FROM operations WHERE caller=? AND request_key=?`, caller, key)
	return op, n == 1, err
}
func (s *Store) GetOperation(ctx context.Context, id string) (Operation, error) {
	return s.operationQuery(ctx, `SELECT id,caller,request_key,payload,state,vmid,http_status,result FROM operations WHERE id=?`, id)
}
func (s *Store) operationQuery(ctx context.Context, query string, args ...any) (Operation, error) {
	var op Operation
	var vmid sql.NullInt64
	err := s.DB.QueryRowContext(ctx, query, args...).Scan(&op.ID, &op.Caller, &op.Key, &op.Payload, &op.State, &vmid, &op.HTTPStatus, &op.Result)
	if vmid.Valid {
		n := int(vmid.Int64)
		op.VMID = &n
	}
	return op, err
}
func (s *Store) FinishOperation(ctx context.Context, id, state string, status int, result string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE operations SET state=?,http_status=?,result=? WHERE id=?`, state, status, result, id)
	return err
}

func operationID(ctx context.Context) string {
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
}

// sandboxOperationTransaction binds allocation and operation discovery in one
// transaction. A crash cannot leave an allocated sandbox absent from its key.
func (s *Store) sandboxOperationTransaction(ctx context.Context) (*sql.Tx, error) {
	if operationID(ctx) == "" {
		return nil, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var state string
	var vmid sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT state,vmid FROM operations WHERE id=?`, operationID(ctx)).Scan(&state, &vmid); err != nil {
		tx.Rollback()
		return nil, err
	}
	if state != "accepted" || vmid.Valid {
		tx.Rollback()
		return nil, errors.New("operation is no longer accepting an allocation")
	}
	return tx, nil
}
