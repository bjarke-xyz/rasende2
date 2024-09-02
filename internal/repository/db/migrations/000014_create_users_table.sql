-- +goose Up
CREATE TABLE IF NOT EXISTS users(
    id INTEGER PRIMARY KEY,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    email TEXT NOT NULL UNIQUE,
    email_confirmed BOOLEAN NOT NULL DEFAULT 0,
    password_hash TEXT,
    is_admin BOOLEAN NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS ix_users_email ON users(email);

CREATE TABLE IF NOT EXISTS magic_links(
    id INTEGER PRIMARY KEY,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_id INTEGER NOT NULL,
    otp_hash TEXT NOT NULL,
    link_code TEXT NOT NULL UNIQUE,
    expires_at DATETIME NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id)
);
CREATE INDEX IF NOT EXISTS ix_magic_links_user_id_expires_at ON magic_links(user_id, expires_at);
CREATE INDEX IF NOT EXISTS ix_magic_links_link_code ON magic_links(link_code);
CREATE INDEX IF NOT EXISTS ix_magic_links_expires_at ON magic_links(expires_at);

-- +goose Down
DROP TABLE IF EXISTS users;
DROP INDEX IF EXISTS ix_users_email;

DROP TABLE IF EXISTS magic_links;
DROP INDEX IF EXISTS ix_magic_links_user_id_expires_at;
DROP INDEX IF EXISTS ix_magic_links_link_code;