package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/joho/godotenv/autoload"
	_ "modernc.org/sqlite"

	"github.com/ninesl/vinyl-keeper/vinyl"
)

func main() {
	//if len(os.Args) < 2 {
	//log.Fatalf("missing image path; expected: go run . -path/to/image.jpg")
	//}
	//imagePath := os.Args[1]
	//if imagePath == "" {
	//log.Fatalf("empty image path")
	//}
	//if imagePath[0] == '-' {
	//imagePath = imagePath[1:]
	//}
	//imageData, imageEmbedding := ImageTupleFromPath(imagePath)
	//var params = vinyl.RegisterVinylParams{
	//VinylTitle:        "Kill 'Em All",
	//VinylArtist:       "Metallica",
	//VinylPressingYear: 2022,
	//FirstPressing:     0,
	//ImageExtension:    PNG,
	//CoverRawBlob:      imageData,
	//CoverEmbedding:    EmbeddingToBlob(imageEmbedding),
	//}

	// TODO: test 2 diff images into a new Keeper and make sure other images
	// close to it an match the embeddings correctly

	//ctx := context.Background()
	//queries := PrepareQueries(ctx)
	//defer queries.Close()
	//record, err := queries.RegisterVinyl(ctx, params)
	//if err != nil {
	//log.Fatalf("register vinyl: %v", err)
	//}
	//fmt.Println(String(record))

	symbolicArgs, err := RegisterUniqueVinylQueryParams("Symbolic", "Death")
	if err != nil {
		log.Fatalf("determining query params: %v", err)
	}

	k, err := NewKeeper()
	if err != nil {
		log.Fatalf("new keeper: %v", err)
	}
	symbolic, err := k.RegisterVinylUnique(symbolicArgs)
	if err != nil {
		log.Fatalf("register vinyl: %v", err)
	}

	err = SaveAlbumCoverToDisk(symbolic)
	if err != nil {
		log.Fatalf("save album cover to disk: %v", err)
	}
	fmt.Println(String(symbolic))
}

func SaveAlbumCoverToDisk(v vinyl.VinylUnique) error {
	filename := fmt.Sprintf("%s-%s.%s", v.VinylArtist, v.VinylTitle, v.ImageExtension)
	return os.WriteFile(filename, v.CoverRawBlob, 0644)
}

const (
	PNG  = "png"
	JPG  = "jpg"
	JPEG = "jpeg"
)

type ImageData []byte

func ImageTupleFromPath(path string) (ImageData, Embedding) {
	imgData, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read image: %v", err)
	}
	embedding, err := RequestEmbedding(imgData)
	if err != nil {
		log.Fatal(err)
	}
	return imgData, embedding
}

func PrepareQueries(ctx context.Context) *vinyl.Queries {
	db, err := openDB(ctx)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()
	queries, err := vinyl.Prepare(ctx, db)
	if err != nil {
		log.Fatalf("prepare queries: %v", err)
	}
	return queries
}

func String(record vinyl.VinylUnique) string {
	// for smaller printing
	record.CoverRawBlob = []byte{}
	record.CoverEmbedding = []byte{}
	return fmt.Sprintf("%+#v\n", record)
}

func SaveAlbumCover(albumTitle, artist string) error {
	resp, err := requestDiscogs(albumTitle, artist)
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s-%s.%s", resp.artist, resp.title, resp.extension)
	err = os.WriteFile(filename, resp.rawCoverData, 0644)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}

// openDB returns the sqlite3 *sql.DB via the DB_PATH env variable
func openDB(ctx context.Context) (*sql.DB, error) {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./vinyls.db"
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	if _, err = db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return db, nil
}
