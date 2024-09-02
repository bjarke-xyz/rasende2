package core

type Cache interface {
	Insert(key string, value string, expirationMinutes int) error
	InsertObj(key string, value any, expirationMinutes int) error

	Get(key string) (string, error)
	GetObj(key string, target any) (bool, error)

	DeleteExpired() error
	DeleteByPrefix(prefix string) error
}
