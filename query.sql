-- name: CreateUser :one
INSERT INTO users(
    user_name, date_created
) VALUES (?, ?)
RETURNING *;

-- name: RegisterVinyl :one
INSERT INTO vinyl_unique(
    vinyl_title, vinyl_artist, vinyl_pressing_year,
    first_pressing, image_extension,
    cover_raw_blob, cover_embedding
) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAllVinyls :many
SELECT * FROM vinyl_unique;