-- name: CreateUser :one
INSERT INTO users(user_name) VALUES (?)
RETURNING *;

-- name: ListUsers :many
SELECT * FROM users ORDER BY user_name ASC;

-- name: GetUserByID :one
SELECT * FROM users WHERE user_id = ?;

-- name: RegisterVinylUnique :one
INSERT INTO vinyl_unique(
    vinyl_title,
    vinyl_artist,
    master_id,
    master_year,
    styles,
    genres
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT
DO UPDATE SET
    master_year = COALESCE(excluded.master_year, vinyl_unique.master_year),
    styles = COALESCE(excluded.styles, vinyl_unique.styles),
    genres = COALESCE(excluded.genres, vinyl_unique.genres)
RETURNING *;

-- name: UpsertVinylRelease :one
INSERT INTO vinyl_release(
    vinyl_id,
    release_id,
    lowest_price,
    price_last_updated,
    country,
    notes,
    released,
    master_release,
    resource_uri,
    image_extension,
    cover_raw_blob,
    cover_embedding
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(vinyl_id, release_id)
DO UPDATE SET
    lowest_price = excluded.lowest_price,
    price_last_updated = excluded.price_last_updated,
    country = excluded.country,
    notes = excluded.notes,
    released = excluded.released,
    master_release = excluded.master_release,
    resource_uri = excluded.resource_uri,
    image_extension = excluded.image_extension,
    cover_raw_blob = excluded.cover_raw_blob,
    cover_embedding = excluded.cover_embedding
RETURNING *;

-- name: GetAllVinylRecords :many
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.master_year,
    vu.styles,
    vu.genres,
    COALESCE(vu.master_year, CAST(substr(vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    vr.country,
    vr.lowest_price AS recent_price,
    COALESCE(vr.released, CAST(vu.master_year AS TEXT), '') AS released,
    COALESCE(vr.image_extension, '') AS image_extension,
    COALESCE(vr.cover_raw_blob, X'') AS cover_raw_blob,
    COALESCE(vr.cover_embedding, X'') AS cover_embedding
FROM vinyl_unique vu
LEFT JOIN vinyl_release vr
    ON vr.vinyl_id = vu.vinyl_id
   AND vr.release_id = 0
ORDER BY vu.vinyl_artist ASC, vu.vinyl_title ASC;

-- name: GetVinylRecordByID :one
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.master_year,
    vu.styles,
    vu.genres,
    COALESCE(vu.master_year, CAST(substr(vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    vr.country,
    vr.lowest_price AS recent_price,
    COALESCE(vr.released, CAST(vu.master_year AS TEXT), '') AS released,
    COALESCE(vr.image_extension, '') AS image_extension,
    COALESCE(vr.cover_raw_blob, X'') AS cover_raw_blob,
    COALESCE(vr.cover_embedding, X'') AS cover_embedding
FROM vinyl_unique vu
LEFT JOIN vinyl_release vr
    ON vr.vinyl_id = vu.vinyl_id
   AND vr.release_id = 0
WHERE vu.vinyl_id = ?;

-- name: GetPrimaryReleaseID :one
SELECT release_id
FROM vinyl_release
WHERE vinyl_id = ? AND master_release = 1
   AND release_id != 0
LIMIT 1;

-- name: UserOwnsMaster :one
SELECT EXISTS(
    SELECT 1
    FROM vinyl_plays vp
    JOIN vinyl_unique vu ON vu.vinyl_id = vp.vinyl_id
    WHERE vp.user_id = ?
      AND vu.master_id = ?
);

-- name: GetMyVinyl :many
WITH plays AS (
    SELECT
        vp.user_id,
        vp.vinyl_id,
        CAST(SUM(CASE WHEN vp.play >= 1 THEN 1 ELSE 0 END) AS INTEGER) AS plays,
        CAST(MIN(vp.played_date) AS TEXT) AS first_played,
        CAST(MAX(vp.played_date) AS TEXT) AS last_played,
        (
            SELECT vp2.release_id
            FROM vinyl_plays vp2
            WHERE vp2.user_id = vp.user_id
              AND vp2.vinyl_id = vp.vinyl_id
            ORDER BY vp2.played_date DESC, vp2.play DESC
            LIMIT 1
        ) AS release_id
    FROM vinyl_plays vp
    WHERE vp.user_id = ?
    GROUP BY vp.user_id, vp.vinyl_id
)
SELECT
    p.vinyl_id,
    p.plays,
    p.first_played,
    p.last_played,
    p.release_id,
    COALESCE(vu.master_year, CAST(substr(master_vr.released, 1, 4) AS INTEGER), CAST(substr(vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    CASE WHEN p.release_id = 0 THEN NULL ELSE COALESCE(NULLIF(vrc.country, ''), vr.country) END AS release_country,
    COALESCE(vr.released, CAST(vu.master_year AS TEXT), '') AS released,
    COALESCE(vr.image_extension, '') AS image_extension,
    CASE WHEN p.release_id = 0 THEN NULL ELSE vr.lowest_price END AS lowest_price,
    vrc.label,
    vrc.release_format,
    vrc.cover_uri,
    CASE WHEN length(vr.cover_raw_blob) > 0 THEN 1 ELSE 0 END AS has_blob,
    CASE WHEN p.release_id = 0 THEN NULL ELSE vr.notes END AS notes
FROM plays p
JOIN vinyl_unique vu ON vu.vinyl_id = p.vinyl_id
LEFT JOIN vinyl_release vr
  ON vr.vinyl_id = p.vinyl_id
 AND vr.release_id = p.release_id
LEFT JOIN vinyl_release master_vr
  ON master_vr.vinyl_id = p.vinyl_id
 AND master_vr.release_id = 0
LEFT JOIN vinyl_releases_check vrc
  ON vrc.vinyl_id = p.vinyl_id
 AND vrc.release_id = p.release_id
ORDER BY p.last_played DESC;

-- name: FindExistingVinylIDByMasterID :one
SELECT vu.vinyl_id
FROM vinyl_unique vu
JOIN vinyl_release vr ON vr.vinyl_id = vu.vinyl_id AND vr.release_id = 0
WHERE vu.master_id = ?
LIMIT 1;

-- name: ListOwnedPressings :many
SELECT DISTINCT vinyl_id, release_id
FROM vinyl_plays
WHERE user_id = ? AND release_id != 0;

-- name: UpsertVinylReleaseCheck :exec
INSERT INTO vinyl_releases_check(vinyl_id, release_id, label, country, release_format, released_year, cover_uri)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(vinyl_id, release_id) DO UPDATE SET
    label = excluded.label,
    country = excluded.country,
    release_format = excluded.release_format,
    released_year = excluded.released_year,
    cover_uri = excluded.cover_uri;

-- name: GetReleaseBlobLengths :one
SELECT length(cover_embedding) AS cover_embedding_len, length(cover_raw_blob) AS cover_raw_blob_len
FROM vinyl_release
WHERE vinyl_id = ? AND release_id = ?
LIMIT 1;

-- name: GetReleaseMasterFlag :one
SELECT master_release
FROM vinyl_release
WHERE vinyl_id = ? AND release_id = ?
LIMIT 1;

-- name: DeleteUserVinylPlays :exec
DELETE FROM vinyl_plays
WHERE user_id = ? AND vinyl_id = ?;

-- name: ListUserVinylPlayDates :many
SELECT play, played_date
FROM vinyl_plays
WHERE user_id = ? AND vinyl_id = ?
ORDER BY played_date ASC, play ASC;

-- name: GetReleaseCover :one
SELECT image_extension, cover_raw_blob
FROM vinyl_release
WHERE vinyl_id = ? AND release_id = ?
LIMIT 1;

-- name: GetCurrentReleaseID :one
SELECT release_id
FROM vinyl_plays
WHERE user_id = ? AND vinyl_id = ?
ORDER BY played_date DESC, play DESC
LIMIT 1;

-- name: ListReleaseScanRows :many
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.styles,
    vu.genres,
    COALESCE(vu.master_year, CAST(substr(master_vr.released, 1, 4) AS INTEGER), CAST(substr(vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    COALESCE(NULLIF(vrc.country, ''), vr.country) AS country,
    vr.released,
    vr.lowest_price,
    vr.image_extension,
    vr.cover_embedding,
    vr.release_id,
    vrc.label,
    vrc.release_format,
    COALESCE(NULLIF(vrc.country, ''), vr.country) AS release_country,
    vrc.cover_uri,
    CASE WHEN length(vr.cover_raw_blob) > 0 THEN 1 ELSE 0 END AS has_blob,
    vr.notes
FROM vinyl_release vr
JOIN vinyl_unique vu ON vu.vinyl_id = vr.vinyl_id
LEFT JOIN vinyl_releases_check vrc
    ON vrc.vinyl_id = vr.vinyl_id
   AND vrc.release_id = vr.release_id
LEFT JOIN vinyl_release master_vr
    ON master_vr.vinyl_id = vr.vinyl_id
   AND master_vr.release_id = 0
WHERE length(vr.cover_embedding) > 0
  AND length(vr.cover_raw_blob) > 0;

-- name: ListUserScanRows :many
WITH current_release AS (
    SELECT vinyl_id, release_id
    FROM (
        SELECT
            vinyl_id,
            release_id,
            ROW_NUMBER() OVER (PARTITION BY vinyl_id ORDER BY played_date DESC, play DESC) AS rn
        FROM vinyl_plays
        WHERE user_id = ?
    )
    WHERE rn = 1
)
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.styles,
    vu.genres,
    COALESCE(vu.master_year, CAST(substr(master_vr.released, 1, 4) AS INTEGER), CAST(substr(scan_vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN master_vr.country ELSE COALESCE(NULLIF(vrc.country, ''), scan_vr.country) END AS country,
    COALESCE(master_vr.released, scan_vr.released, CAST(vu.master_year AS TEXT), '') AS released,
    scan_vr.lowest_price,
    scan_vr.image_extension,
    scan_vr.cover_embedding,
    scan_vr.release_id,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN NULL ELSE vrc.label END AS label,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN NULL ELSE vrc.release_format END AS release_format,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN NULL ELSE COALESCE(NULLIF(vrc.country, ''), scan_vr.country) END AS release_country,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN NULL ELSE vrc.cover_uri END AS cover_uri,
    CASE WHEN length(scan_vr.cover_raw_blob) > 0 THEN 1 ELSE 0 END AS has_blob,
    CASE WHEN cr.release_id IS NULL OR cr.release_id = 0 THEN NULL ELSE scan_vr.notes END AS notes
FROM vinyl_unique vu
JOIN vinyl_release master_vr
    ON master_vr.vinyl_id = vu.vinyl_id
   AND master_vr.release_id = 0
LEFT JOIN current_release cr
    ON cr.vinyl_id = vu.vinyl_id
LEFT JOIN vinyl_release owned_vr
    ON owned_vr.vinyl_id = vu.vinyl_id
   AND owned_vr.release_id = cr.release_id
   AND cr.release_id != 0
JOIN vinyl_release scan_vr
    ON scan_vr.vinyl_id = vu.vinyl_id
   AND scan_vr.release_id = CASE WHEN owned_vr.release_id IS NOT NULL THEN owned_vr.release_id ELSE 0 END
LEFT JOIN vinyl_releases_check vrc
    ON vrc.vinyl_id = scan_vr.vinyl_id
   AND vrc.release_id = scan_vr.release_id
WHERE length(scan_vr.cover_embedding) > 0
  AND length(scan_vr.cover_raw_blob) > 0;

-- name: GetReleaseCandidateRow :one
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.styles,
    vu.genres,
    COALESCE(vu.master_year, CAST(substr(master_vr.released, 1, 4) AS INTEGER), CAST(substr(vr.released, 1, 4) AS INTEGER), 0) AS vinyl_pressing_year,
    COALESCE(NULLIF(vrc.country, ''), vr.country) AS country,
    vr.released,
    vr.lowest_price,
    vr.image_extension,
    vr.cover_embedding,
    vr.release_id,
    vrc.label,
    vrc.release_format,
    COALESCE(NULLIF(vrc.country, ''), vr.country) AS release_country,
    vrc.cover_uri,
    CASE WHEN length(vr.cover_raw_blob) > 0 THEN 1 ELSE 0 END AS has_blob,
    vr.notes
FROM vinyl_release vr
JOIN vinyl_unique vu ON vu.vinyl_id = vr.vinyl_id
LEFT JOIN vinyl_releases_check vrc
    ON vrc.vinyl_id = vr.vinyl_id
   AND vrc.release_id = vr.release_id
LEFT JOIN vinyl_release master_vr
    ON master_vr.vinyl_id = vr.vinyl_id
   AND master_vr.release_id = 0
WHERE vr.vinyl_id = ? AND vr.release_id = ?
LIMIT 1;

-- name: ListPressingOptionRows :many
SELECT
    vu.vinyl_id,
    vu.vinyl_title,
    vu.vinyl_artist,
    vu.master_id,
    vu.styles,
    vu.genres,
    COALESCE(vrc.released_year, 0) AS vinyl_pressing_year,
    NULLIF(vrc.country, '') AS country,
    COALESCE(vr.released, '') AS released,
    vr.lowest_price,
    COALESCE(vr.image_extension, '') AS image_extension,
    vrc.release_id,
    NULLIF(vrc.label, '') AS label,
    NULLIF(vrc.release_format, '') AS release_format,
    NULLIF(vrc.country, '') AS release_country,
    NULLIF(vrc.cover_uri, '') AS cover_uri,
    CASE WHEN length(vr.cover_raw_blob) > 0 THEN 1 ELSE 0 END AS has_blob,
    vr.notes,
    CASE WHEN vrc.release_id = (
        SELECT vp.release_id
        FROM vinyl_plays vp
        WHERE vp.user_id = ? AND vp.vinyl_id = ?
        ORDER BY vp.played_date DESC, vp.play DESC
        LIMIT 1
    ) THEN 1 ELSE 0 END AS is_current
FROM vinyl_releases_check vrc
JOIN vinyl_unique vu ON vu.vinyl_id = vrc.vinyl_id
LEFT JOIN vinyl_release vr
    ON vr.vinyl_id = vrc.vinyl_id
   AND vr.release_id = vrc.release_id
WHERE vrc.vinyl_id = ?
ORDER BY vrc.released_year ASC, vrc.release_id ASC;

-- name: EnsureOwnershipPlay :exec
INSERT OR IGNORE INTO vinyl_plays(user_id, vinyl_id, release_id, play, played_date)
VALUES (?, ?, ?, 0, ?);

-- name: NextPlayNumber :one
SELECT COALESCE(MAX(play), 0) + 1
FROM vinyl_plays
WHERE user_id = ?
  AND vinyl_id = ?
  AND release_id = ?;

-- name: InsertVinylPlay :exec
INSERT INTO vinyl_plays(user_id, vinyl_id, release_id, play, played_date)
VALUES (?, ?, ?, ?, ?);

-- name: GetUserVinylPlays :many
SELECT
    user_id,
    vinyl_id,
    CAST(SUM(CASE WHEN play >= 1 THEN 1 ELSE 0 END) AS INTEGER) AS plays,
    CAST(MIN(played_date) AS TEXT) AS first_played,
    CAST(MAX(played_date) AS TEXT) AS last_played
FROM vinyl_plays
WHERE user_id = ?
GROUP BY user_id, vinyl_id
ORDER BY last_played DESC;

-- name: GetAllUserVinylPlays :many
SELECT
    user_id,
    vinyl_id,
    CAST(SUM(CASE WHEN play >= 1 THEN 1 ELSE 0 END) AS INTEGER) AS plays,
    CAST(MIN(played_date) AS TEXT) AS first_played,
    CAST(MAX(played_date) AS TEXT) AS last_played
FROM vinyl_plays
GROUP BY user_id, vinyl_id;

-- name: DeleteVinyl :exec
DELETE FROM vinyl_unique WHERE vinyl_id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE user_id = ?;
