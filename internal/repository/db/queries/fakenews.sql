-- name: GetAllFakeNews :many
SELECT * FROM fake_news;

-- name: SetFakeNewsExternalId :exec
UPDATE fake_news SET external_id = ? WHERE site_id = ? AND title = ?;