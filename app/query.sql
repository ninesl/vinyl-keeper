-- name: CreateUser :one
INSERT INTO users(user_name) VALUES (?)
RETURNING *;

-- name: RegisterVinyl :one
INSERT INTO vinyl_unique(
    vinyl_title, vinyl_artist, vinyl_pressing_year,
    first_pressing, discogs_master_id, styles, genres,
    image_extension, cover_raw_blob, cover_embedding
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
-- RETURNING only fires on rows that are written. DO NOTHING suppresses the
-- write, so RETURNING returns nothing. The no-op SET forces a write, giving
-- RETURNING the row to return. https://sqlite.org/forum/forumpost/6c153c0a6e224091
ON CONFLICT(vinyl_title, vinyl_artist, vinyl_pressing_year)
DO UPDATE SET vinyl_title = vinyl_title
RETURNING *;

-- name: GetAllVinyls :many
SELECT * FROM vinyl_unique;

-- name: PlayVinylCollection :one
INSERT INTO user_vinyl_plays (user_id, vinyl_id, plays, first_played, last_played)
VALUES (?, ?, 1, date('now'), date('now'))
ON CONFLICT(user_id, vinyl_id)
DO UPDATE SET
    plays = plays + 1,
    first_played = COALESCE(first_played, date('now')),
    last_played = date('now')
RETURNING *;

-- name: RecordVinylCollection :one
INSERT INTO user_vinyl_plays (user_id, vinyl_id, plays)
VALUES (?, ?, 0)
ON CONFLICT(user_id, vinyl_id)
DO UPDATE SET 
    plays = plays + 1,
    first_played = COALESCE(first_played, date('now')),
    last_played = date('now')
RETURNING *;

-- name: GetUserVinylPlays :many
SELECT * FROM user_vinyl_plays WHERE user_id = ? ORDER BY last_played DESC;

-- name: GetAllUserVinylPlays :many
SELECT * FROM user_vinyl_plays;

-- name: DeleteVinyl :exec
DELETE FROM vinyl_unique WHERE vinyl_id = ?;
