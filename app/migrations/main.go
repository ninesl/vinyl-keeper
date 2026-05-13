package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

const dbFileName = "vinylkeeper.db"

type migrationRow struct {
	VinylID        int64
	Title          string
	Artist         string
	MasterID       sql.NullInt64
	MasterYear     sql.NullInt64
	Styles         sql.NullString
	Genres         sql.NullString
	ImageExtension string
	CoverRawBlob   []byte
	CoverEmbedding []byte
}

func main() {
	limit := flag.Int("limit", 0, "maximum rows to migrate; 0 means all")
	dryRun := flag.Bool("dry-run", false, "print rows that would get release_id=0 without updating")
	flag.Parse()

	ctx := context.Background()
	dbPath, err := databasePath()
	if err != nil {
		log.Fatal(err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		log.Fatalf("open sqlite %q: %v", dbPath, err)
	}
	defer db.Close()

	columns, err := tableColumns(ctx, db, "vinyl_unique")
	if err != nil {
		log.Fatal(err)
	}
	if !columns["image_extension"] || !columns["cover_raw_blob"] || !columns["cover_embedding"] {
		log.Printf("[migration] vinyl_unique image columns already absent; nothing to move")
		return
	}

	rows, err := rowsWithExistingMasterData(ctx, db, *limit)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("[migration] existing master rows=%d dry_run=%v", len(rows), *dryRun)
	if *dryRun {
		for _, row := range rows {
			log.Printf("[migration] would upsert release_id=0 vinyl_id=%d title=%q artist=%q", row.VinylID, row.Title, row.Artist)
		}
		log.Printf("[migration] would rebuild vinyl_unique without image_extension/cover_raw_blob/cover_embedding")
		return
	}

	migrated, err := migrateMasterFallbackReleases(ctx, db, rows, columns)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[migration] done migrated=%d", migrated)
}

func databasePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("DB_PATH")); path != "" {
		return path, nil
	}
	return "", fmt.Errorf("DB_PATH is required; expected canonical database path, for example ./data/%s", dbFileName)
}

func sqliteDSN(dbPath string) string {
	if strings.Contains(dbPath, "?") {
		return dbPath + "&_pragma=foreign_keys(1)"
	}
	return dbPath + "?_pragma=foreign_keys(1)"
}

func rowsWithExistingMasterData(ctx context.Context, db *sql.DB, limit int) ([]migrationRow, error) {
	query := `
		SELECT vinyl_id, vinyl_title, vinyl_artist, master_id, master_year, styles, genres, image_extension, cover_raw_blob, cover_embedding
		FROM vinyl_unique
		WHERE image_extension IS NOT NULL
		  AND image_extension != ''
		  AND length(cover_raw_blob) > 0
		  AND length(cover_embedding) > 0
		ORDER BY vinyl_id ASC`
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []migrationRow{}
	for rows.Next() {
		var row migrationRow
		if err := rows.Scan(&row.VinylID, &row.Title, &row.Artist, &row.MasterID, &row.MasterYear, &row.Styles, &row.Genres, &row.ImageExtension, &row.CoverRawBlob, &row.CoverEmbedding); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func migrateMasterFallbackReleases(ctx context.Context, db *sql.DB, rows []migrationRow, columns map[string]bool) (int, error) {
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return 0, fmt.Errorf("disable foreign keys: %w", err)
	}
	defer db.ExecContext(ctx, "PRAGMA foreign_keys = ON")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO vinyl_release(
			vinyl_id, release_id, lowest_price, price_last_updated, country, notes, released,
			master_release, resource_uri, image_extension, cover_raw_blob, cover_embedding
		) VALUES (?, 0, NULL, NULL, NULL, NULL, ?, 1, ?, ?, ?, ?)
		ON CONFLICT(vinyl_id, release_id) DO UPDATE SET
			released = excluded.released,
			master_release = 1,
			resource_uri = excluded.resource_uri,
			image_extension = excluded.image_extension,
			cover_raw_blob = excluded.cover_raw_blob,
			cover_embedding = excluded.cover_embedding
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, row := range rows {
		released := "0"
		if row.MasterYear.Valid && row.MasterYear.Int64 > 0 {
			released = fmt.Sprintf("%d", row.MasterYear.Int64)
		}
		resourceURI := fmt.Sprintf("vinylkeeper://vinyl/%d/master", row.VinylID)
		if _, err := stmt.ExecContext(ctx, row.VinylID, released, resourceURI, row.ImageExtension, row.CoverRawBlob, row.CoverEmbedding); err != nil {
			return 0, fmt.Errorf("upsert master fallback vinyl_id=%d: %w", row.VinylID, err)
		}
	}
	if err := rebuildVinylUniqueWithoutImages(ctx, tx, columns); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_key_check"); err != nil {
		return 0, fmt.Errorf("foreign key check after migration: %w", err)
	}
	return len(rows), nil
}

func tableColumns(ctx context.Context, db *sql.DB, tableName string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var colType string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func rebuildVinylUniqueWithoutImages(ctx context.Context, tx *sql.Tx, columns map[string]bool) error {
	masterIDExpr := "NULL"
	if columns["master_id"] {
		masterIDExpr = "master_id"
	}
	masterYearExpr := "NULL"
	if columns["master_year"] {
		masterYearExpr = "master_year"
	}
	stylesExpr := "NULL"
	if columns["styles"] {
		stylesExpr = "styles"
	}
	genresExpr := "NULL"
	if columns["genres"] {
		genresExpr = "genres"
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE vinyl_unique_new(
			vinyl_id INTEGER PRIMARY KEY AUTOINCREMENT,
			vinyl_title TEXT NOT NULL,
			vinyl_artist TEXT NOT NULL,
			master_id INTEGER,
			master_year INTEGER,
			styles TEXT,
			genres TEXT,
			UNIQUE (vinyl_title, vinyl_artist)
		)
	`); err != nil {
		return fmt.Errorf("create vinyl_unique_new: %w", err)
	}

	insertStmt := fmt.Sprintf(`
		INSERT INTO vinyl_unique_new(vinyl_id, vinyl_title, vinyl_artist, master_id, master_year, styles, genres)
		SELECT vinyl_id, vinyl_title, vinyl_artist, %s, %s, %s, %s
		FROM vinyl_unique
	`, masterIDExpr, masterYearExpr, stylesExpr, genresExpr)
	if _, err := tx.ExecContext(ctx, insertStmt); err != nil {
		return fmt.Errorf("copy vinyl_unique: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE vinyl_unique"); err != nil {
		return fmt.Errorf("drop old vinyl_unique: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE vinyl_unique_new RENAME TO vinyl_unique"); err != nil {
		return fmt.Errorf("rename vinyl_unique_new: %w", err)
	}
	return nil
}
