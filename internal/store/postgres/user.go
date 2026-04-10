package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yourorg/cloudctrl/internal/model"
)

type UserStore struct {
	pool pooler
}

func NewUserStore(pool pooler) *UserStore {
	return &UserStore{pool: pool}
}

func (s *UserStore) Create(ctx context.Context, u *model.User) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, password_hash, name, role, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID, u.TenantID, u.Email, u.PasswordHash, u.Name, u.Role, u.Active,
	)
	if err != nil {
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *UserStore) GetByID(ctx context.Context, tenantID, userID uuid.UUID) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, email, password_hash, name, role, active,
		       api_key_hash, last_login_at, created_at, updated_at
		FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID,
	).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
		&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByEmail(ctx context.Context, tenantID uuid.UUID, email string) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, email, password_hash, name, role, active,
		       api_key_hash, last_login_at, created_at, updated_at
		FROM users WHERE email = $1 AND tenant_id = $2`, email, tenantID,
	).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
		&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}

// GetByEmailGlobal searches across all tenants — used for login when tenant is unknown.
func (s *UserStore) GetByEmailGlobal(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.tenant_id, u.email, u.password_hash, u.name, u.role, u.active,
		       u.api_key_hash, u.last_login_at, u.created_at, u.updated_at
		FROM users u
		JOIN tenants t ON t.id = u.tenant_id
		WHERE u.email = $1 AND u.active = true AND t.active = true
		LIMIT 1`, email,
	).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
		&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by email global: %w", err)
	}
	return &u, nil
}

func (s *UserStore) GetByAPIKey(ctx context.Context, apiKeyHash string) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.tenant_id, u.email, u.password_hash, u.name, u.role, u.active,
		       u.api_key_hash, u.last_login_at, u.created_at, u.updated_at
		FROM users u
		JOIN tenants t ON t.id = u.tenant_id
		WHERE u.api_key_hash = $1 AND u.active = true AND t.active = true`, apiKeyHash,
	).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
		&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by api key: %w", err)
	}
	return &u, nil
}

func (s *UserStore) List(ctx context.Context, tenantID uuid.UUID) ([]*model.User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, email, password_hash, name, role, active,
		       api_key_hash, last_login_at, created_at, updated_at
		FROM users WHERE tenant_id = $1
		ORDER BY created_at DESC`, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(
			&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
			&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, &u)
	}
	return users, rows.Err()
}

func (s *UserStore) Update(ctx context.Context, tenantID, userID uuid.UUID, input *model.UpdateUserInput) error {
	setClauses := []string{}
	args := []interface{}{}
	argIdx := 1

	if input.Email != nil {
		setClauses = append(setClauses, fmt.Sprintf("email = $%d", argIdx))
		args = append(args, *input.Email)
		argIdx++
	}
	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *input.Name)
		argIdx++
	}
	if input.Role != nil {
		setClauses = append(setClauses, fmt.Sprintf("role = $%d", argIdx))
		args = append(args, *input.Role)
		argIdx++
	}
	if input.Active != nil {
		setClauses = append(setClauses, fmt.Sprintf("active = $%d", argIdx))
		args = append(args, *input.Active)
		argIdx++
	}

	if len(setClauses) == 0 {
		return nil
	}

	query := fmt.Sprintf("UPDATE users SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(setClauses, ", "), argIdx, argIdx+1)
	args = append(args, userID, tenantID)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *UserStore) UpdatePassword(ctx context.Context, tenantID, userID uuid.UUID, passwordHash string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE users SET password_hash = $1 WHERE id = $2 AND tenant_id = $3`,
		passwordHash, userID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *UserStore) UpdateAPIKey(ctx context.Context, tenantID, userID uuid.UUID, apiKeyHash string) error {
	result, err := s.pool.Exec(ctx, `
		UPDATE users SET api_key_hash = $1 WHERE id = $2 AND tenant_id = $3`,
		apiKeyHash, userID, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update api key: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func (s *UserStore) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users SET last_login_at = NOW() WHERE id = $1`, userID)
	return err
}

func (s *UserStore) Delete(ctx context.Context, tenantID, userID uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `
		DELETE FROM users WHERE id = $1 AND tenant_id = $2`, userID, tenantID)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}
// GetByIDGlobal retrieves a user by ID without tenant scoping.
// Used during refresh token flow where we only have the user ID.
func (s *UserStore) GetByIDGlobal(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	var u model.User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.tenant_id, u.email, u.password_hash, u.name, u.role, u.active,
		       u.api_key_hash, u.last_login_at, u.created_at, u.updated_at
		FROM users u
		JOIN tenants t ON t.id = u.tenant_id
		WHERE u.id = $1 AND u.active = true AND t.active = true`, userID,
	).Scan(
		&u.ID, &u.TenantID, &u.Email, &u.PasswordHash, &u.Name, &u.Role, &u.Active,
		&u.APIKeyHash, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id global: %w", err)
	}
	return &u, nil
}

// Count returns the number of users in a tenant.
func (s *UserStore) Count(ctx context.Context, tenantID uuid.UUID) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE tenant_id = $1`, tenantID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}
