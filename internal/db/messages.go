// ABOUTME: Message database operations for the shared messagebox.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Message represents an append-only message stored in the messagebox.
type Message struct {
	ID        int64
	Timestamp time.Time
	ScopeType string
	ScopeID   string
	Author    string
	Kind      string
	Text      string
	JSON      string
}

// CreateMessage inserts a new message into the database.
func (s *Store) CreateMessage(ctx context.Context, message Message) (Message, error) {
	if s == nil || s.DB == nil {
		return Message{}, errors.New("db store is nil")
	}
	message.ScopeType = strings.TrimSpace(message.ScopeType)
	if message.ScopeType == "" {
		return Message{}, errors.New("message scope_type is required")
	}
	message.ScopeID = strings.TrimSpace(message.ScopeID)
	if message.ScopeID == "" {
		return Message{}, errors.New("message scope_id is required")
	}
	message.Author = strings.TrimSpace(message.Author)
	message.Kind = strings.TrimSpace(message.Kind)
	message.Text = strings.TrimSpace(message.Text)
	message.JSON = strings.TrimSpace(message.JSON)

	timestamp := message.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	result, err := s.DB.ExecContext(ctx, `INSERT INTO messages (ts, scope_type, scope_id, author, kind, text, json)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		formatTime(timestamp),
		message.ScopeType,
		message.ScopeID,
		nullIfEmpty(message.Author),
		nullIfEmpty(message.Kind),
		nullIfEmpty(message.Text),
		nullIfEmpty(message.JSON),
	)
	if err != nil {
		return Message{}, fmt.Errorf("insert message: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Message{}, fmt.Errorf("message last insert id: %w", err)
	}
	message.ID = id
	message.Timestamp = timestamp
	return message, nil
}

// ListMessagesByScope returns messages for a scope after a given ID.
func (s *Store) ListMessagesByScope(ctx context.Context, scopeType, scopeID string, afterID int64, limit int) ([]Message, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	scopeType = strings.TrimSpace(scopeType)
	scopeID = strings.TrimSpace(scopeID)
	if scopeType == "" {
		return nil, errors.New("message scope_type is required")
	}
	if scopeID == "" {
		return nil, errors.New("message scope_id is required")
	}
	if afterID < 0 {
		return nil, errors.New("after_id must be non-negative")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, scope_type, scope_id, author, kind, text, json
		FROM messages WHERE scope_type = ? AND scope_id = ? AND id > ? ORDER BY id ASC LIMIT ?`,
		scopeType, scopeID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages: %w", err)
	}
	return out, nil
}

// ListMessagesByScopeTail returns the most recent messages for a scope.
func (s *Store) ListMessagesByScopeTail(ctx context.Context, scopeType, scopeID string, limit int) ([]Message, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("db store is nil")
	}
	scopeType = strings.TrimSpace(scopeType)
	scopeID = strings.TrimSpace(scopeID)
	if scopeType == "" {
		return nil, errors.New("message scope_type is required")
	}
	if scopeID == "" {
		return nil, errors.New("message scope_id is required")
	}
	if limit <= 0 {
		return nil, errors.New("limit must be positive")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, ts, scope_type, scope_id, author, kind, text, json
		FROM messages WHERE scope_type = ? AND scope_id = ? ORDER BY id DESC LIMIT ?`,
		scopeType, scopeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages tail: %w", err)
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		msg, err := scanMessageRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate messages tail: %w", err)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func scanMessageRow(scanner interface{ Scan(dest ...any) error }) (Message, error) {
	var msg Message
	var ts string
	var scopeType string
	var scopeID string
	var author sql.NullString
	var kind sql.NullString
	var text sql.NullString
	var jsonPayload sql.NullString
	if err := scanner.Scan(&msg.ID, &ts, &scopeType, &scopeID, &author, &kind, &text, &jsonPayload); err != nil {
		return Message{}, err
	}
	if ts != "" {
		parsed, err := parseTime(ts)
		if err != nil {
			return Message{}, fmt.Errorf("parse message ts: %w", err)
		}
		msg.Timestamp = parsed
	}
	msg.ScopeType = scopeType
	msg.ScopeID = scopeID
	if author.Valid {
		msg.Author = author.String
	}
	if kind.Valid {
		msg.Kind = kind.String
	}
	if text.Valid {
		msg.Text = text.String
	}
	if jsonPayload.Valid {
		msg.JSON = jsonPayload.String
	}
	return msg, nil
}
