package db

import (
	"context"
	"database/sql"
	"time"
)

type User struct {
	ID             int64
	Email          string
	EmailConfirmed bool
	PasswordHash   sql.NullString
	IsAdmin        bool
}

type MagicLink struct {
	ID        int64
	UserID    int64
	OtpHash   string
	LinkCode  string
	ExpiresAt time.Time
}

// Queries holds the hand-written user and magic-link statements backing the
// login flow. Registration is disabled, so only the login path is exercised.
type Queries struct {
	db *sql.DB
}

func NewQueries(db *sql.DB) *Queries {
	return &Queries{db: db}
}

const userColumns = "id, email, email_confirmed, password_hash, is_admin"

func scanUser(row *sql.Row) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Email, &user.EmailConfirmed, &user.PasswordHash, &user.IsAdmin)
	return user, err
}

func (q *Queries) GetUser(ctx context.Context, id int64) (User, error) {
	return scanUser(q.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE id = ? LIMIT 1", id))
}

func (q *Queries) GetUserByEmail(ctx context.Context, email string) (User, error) {
	return scanUser(q.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE email = ? LIMIT 1", email))
}

func (q *Queries) SetUserEmailConfirmed(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx,
		"UPDATE users SET email_confirmed = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND email_confirmed = 0", id)
	return err
}

const magicLinkColumns = "id, user_id, otp_hash, link_code, expires_at"

func (q *Queries) CreateMagicLink(ctx context.Context, userID int64, otpHash string, linkCode string, expiresAt time.Time) error {
	_, err := q.db.ExecContext(ctx,
		"INSERT INTO magic_links (user_id, otp_hash, link_code, expires_at) VALUES (?, ?, ?, ?)",
		userID, otpHash, linkCode, expiresAt)
	return err
}

func (q *Queries) GetLinkByCode(ctx context.Context, linkCode string) (MagicLink, error) {
	var link MagicLink
	err := q.db.QueryRowContext(ctx, "SELECT "+magicLinkColumns+" FROM magic_links WHERE link_code = ? LIMIT 1", linkCode).
		Scan(&link.ID, &link.UserID, &link.OtpHash, &link.LinkCode, &link.ExpiresAt)
	return link, err
}

// GetLinksByUserId returns the user's unexpired magic links.
//
// expires_at is written with a "+00:00" offset while CURRENT_TIMESTAMP has none,
// so the two are compared through datetime() rather than as raw strings.
func (q *Queries) GetLinksByUserId(ctx context.Context, userID int64) ([]MagicLink, error) {
	rows, err := q.db.QueryContext(ctx,
		"SELECT "+magicLinkColumns+" FROM magic_links WHERE user_id = ? AND datetime(expires_at) >= datetime('now')", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []MagicLink{}
	for rows.Next() {
		var link MagicLink
		if err := rows.Scan(&link.ID, &link.UserID, &link.OtpHash, &link.LinkCode, &link.ExpiresAt); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (q *Queries) DeleteMagicLink(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, "DELETE FROM magic_links WHERE id = ?", id)
	return err
}
