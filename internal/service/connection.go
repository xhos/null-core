package service

import (
	"context"
	"fmt"
	"time"

	"null-core/internal/crypto"
	"null-core/internal/db/sqlc"

	"github.com/google/uuid"
)

type ConnectionService interface {
	List(ctx context.Context, userID uuid.UUID) ([]Connection, error)
	Create(ctx context.Context, userID uuid.UUID, provider string, credentialsJSON []byte) (int64, error)
	Delete(ctx context.Context, userID uuid.UUID, id int64) error
}

type Connection struct {
	ID         int64
	Provider   string
	Status     string
	LastSynced *time.Time
	CreatedAt  time.Time
}

type connectionSvc struct {
	queries *sqlc.Queries
	cipher  *crypto.Cipher
}

func newConnectionSvc(queries *sqlc.Queries, cipher *crypto.Cipher) ConnectionService {
	return &connectionSvc{queries: queries, cipher: cipher}
}

func (s *connectionSvc) List(ctx context.Context, userID uuid.UUID) ([]Connection, error) {
	rows, err := s.queries.ListConnectionsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ConnectionService.List: %w", err)
	}
	out := make([]Connection, len(rows))
	for i, r := range rows {
		out[i] = Connection{
			ID:         r.ID,
			Provider:   r.Provider,
			Status:     r.Status,
			LastSynced: r.SyncCursor,
			CreatedAt:  r.CreatedAt,
		}
	}
	return out, nil
}

func (s *connectionSvc) Create(ctx context.Context, userID uuid.UUID, provider string, credentialsJSON []byte) (int64, error) {
	switch provider {
	case "wise", "snaptrade":
	default:
		return 0, fmt.Errorf("ConnectionService.Create: unknown provider %q", provider)
	}

	ciphertext, err := s.cipher.Encrypt(credentialsJSON)
	if err != nil {
		return 0, fmt.Errorf("ConnectionService.Create: encrypt: %w", err)
	}

	row, err := s.queries.CreateConnectedAccount(ctx, sqlc.CreateConnectedAccountParams{
		UserID:      userID,
		Provider:    provider,
		Credentials: ciphertext,
	})
	if err != nil {
		return 0, fmt.Errorf("ConnectionService.Create: insert: %w", err)
	}
	return row.ID, nil
}

func (s *connectionSvc) Delete(ctx context.Context, userID uuid.UUID, id int64) error {
	affected, err := s.queries.DeleteConnectedAccount(ctx, sqlc.DeleteConnectedAccountParams{
		ID:     id,
		UserID: userID,
	})
	if err != nil {
		return fmt.Errorf("ConnectionService.Delete: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("ConnectionService.Delete: not found")
	}
	return nil
}
