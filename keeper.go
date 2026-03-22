package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ninesl/vinyl-keeper/vinyl"
	"modernc.org/sqlite"
	_ "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

//go:embed schema.sql
var schemaSQL string

type Keeper interface {
	RegisterVinylUnique(args vinyl.RegisterVinylParams) (vinyl.VinylUnique, error) // FIXME: this is not discogs? uncertain schema for this yet

	KeepRecord(vinylID int64) error // makes an entry for the record, returns an error if exists already
	PlayRecord(vinylID int64) error // ++ to the numPlays of the vinylID, saves the record if not already logged
	NumPlays(vinylID int64) int     // Number of plays this vinylID has had in this Keeper
	AllVinyl() []vinyl.VinylUnique
	DeleteVinyl(vinylID int64) error // removes vinyl from DB and in-memory caches
}

type keeper struct {
	ctx             context.Context
	userID          int64
	queries         *vinyl.Queries
	vinylLookup     map[int64]vinyl.VinylUnique
	embeddingLookup map[int64]Embedding
	// number of plays each int64 vinylID has had
	numPlays map[int64]int
	mu       sync.RWMutex
}

func NewKeeper() (Keeper, error) {
	k := &keeper{}
	err := k.initKeeper(context.Background())
	return k, err
}

func (k *keeper) AllVinyl() []vinyl.VinylUnique {
	v, err := k.queries.GetAllVinyls(k.ctx)
	if err != nil {
		log.Fatal(err)
	}

	return v
}

func (k *keeper) RegisterVinylUnique(args vinyl.RegisterVinylParams) (vinyl.VinylUnique, error) {
	vinylUnique, err := k.queries.RegisterVinyl(k.ctx, args)
	if err != nil {
		return vinyl.VinylUnique{}, err
	}

	// Decode embedding from the returned vinyl record
	emb, err := EmbeddingFromBlob(vinylUnique.CoverEmbedding)
	if err != nil {
		return vinyl.VinylUnique{}, fmt.Errorf("failed to decode embedding for vinyl %d: %w", vinylUnique.VinylID, err)
	}

	// Update in-memory caches with proper locking
	k.mu.Lock()
	k.vinylLookup[vinylUnique.VinylID] = vinylUnique
	k.embeddingLookup[vinylUnique.VinylID] = emb
	k.mu.Unlock()

	return vinylUnique, nil
}

// needs to impl finding the extension. this abstraction helps with making the consumer API have optional ways to get images
type keeperRegisterVinylParams interface {
	GenerateEmbedding() Embedding
}

// This will not be the final fields for this bc we will need to have this work for discogs eventually
//type KeeperRegisterUniqueVinylParams struct {
//	AlbumTitle, Artist string
//}

// TODO: a smart way to have input for an image.
// in the final app this is simply done from scanning the phone input, so we will just want []byte most likely
// we also want to have the Actual ALBUM cover. this comes from discogs most likely in the final version?
// so we want an image url/image source. I think that pasting a image url from anywhere works. Then it saves the raw image into
// sqlite under a Blob and we can note the image extension pretty easily by parsing the url/source file}

/*
	func (krp KeeperRegisterUniqueVinylParams) rawImageFromURL() ([]byte, string, error) {
		albumCoverURI, err := url.ParseRequestURI(krp.AlbumCoverURI)
		if err != nil {
			return nil, "", err
		}

		// scrape for image
		// if path is n

		return nil, "", nil
	}
*/

type discogsResp struct {
	masterID      int
	title, artist string
	rawCoverData  []byte
	extension     string
	releaseYear   int
	genres        string
	styles        string
}

type discogsSearchResp struct {
	Results []struct {
		MasterID int `json:"master_id"`
	} `json:"results"`
}

type discogsMasterResp struct {
	Title   string   `json:"title"`
	Year    int      `json:"year"`
	Genres  []string `json:"genres"`
	Styles  []string `json:"styles"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Images []struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"images"`
}

func requestDiscogs(albumTitle, artist string) (discogsResp, error) {
	albumTitle = strings.TrimSpace(albumTitle)
	artist = strings.TrimSpace(artist)
	if albumTitle == "" || artist == "" {
		return discogsResp{}, fmt.Errorf("must provide strings for both albumTitle and artist")
	}

	httpClient := http.Client{
		Timeout: time.Second * 10,
	}

	format := "&format=album"
	// Step 1: Search for the release
	searchURL := fmt.Sprintf("https://api.discogs.com/database/search?release_title=%s&artist=%s%s&per_page=1&page=1",
		strings.ReplaceAll(albumTitle, " ", "%20"),
		strings.ReplaceAll(artist, " ", "%20"),
		format)

	log.Printf("Discogs search: %s", searchURL)
	searchResp, err := httpClient.Get(searchURL)
	if err != nil {
		return discogsResp{}, fmt.Errorf("search request failed: %w", err)
	}
	defer searchResp.Body.Close()

	if searchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(searchResp.Body)
		return discogsResp{}, fmt.Errorf("search returned status %d: %s", searchResp.StatusCode, string(body))
	}

	var searchResult discogsSearchResp
	if err := json.NewDecoder(searchResp.Body).Decode(&searchResult); err != nil {
		return discogsResp{}, fmt.Errorf("failed to decode search response: %w", err)
	}

	if len(searchResult.Results) == 0 {
		return discogsResp{}, fmt.Errorf("no results found for album '%s' by artist '%s'", albumTitle, artist)
	}

	masterID := searchResult.Results[0].MasterID
	if masterID == 0 {
		return discogsResp{}, fmt.Errorf("no master_id found in search results")
	}

	// Step 2: Get master release details
	masterURL := fmt.Sprintf("https://api.discogs.com/masters/%d", masterID)
	log.Printf("Discogs master: %s", masterURL)
	masterResp, err := httpClient.Get(masterURL)
	if err != nil {
		return discogsResp{}, fmt.Errorf("master request failed: %w", err)
	}
	defer masterResp.Body.Close()

	if masterResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(masterResp.Body)
		return discogsResp{}, fmt.Errorf("master request returned status %d: %s", masterResp.StatusCode, string(body))
	}

	var masterResult discogsMasterResp
	if err := json.NewDecoder(masterResp.Body).Decode(&masterResult); err != nil {
		return discogsResp{}, fmt.Errorf("failed to decode master response: %w", err)
	}

	// Build comma-separated artist string (no spaces)
	var artistNames []string
	for _, a := range masterResult.Artists {
		artistNames = append(artistNames, a.Name)
	}
	artistStr := strings.Join(artistNames, ",")

	// Build comma-separated genres and styles strings (no spaces)
	genresStr := strings.Join(masterResult.Genres, ",")
	stylesStr := strings.Join(masterResult.Styles, ",")

	// Find primary image URI
	var imageURI string
	for i := 0; i < len(masterResult.Images); i++ {
		if masterResult.Images[i].Type == "primary" {
			imageURI = masterResult.Images[i].URI
			break
		}
	}

	if imageURI == "" {
		return discogsResp{}, fmt.Errorf("no primary image found for master %d", masterID)
	}

	// Step 3: Download the image
	log.Printf("Discogs image: %s", imageURI)
	imageResp, err := httpClient.Get(imageURI)
	if err != nil {
		return discogsResp{}, fmt.Errorf("image download failed: %w", err)
	}
	defer imageResp.Body.Close()

	if imageResp.StatusCode != http.StatusOK {
		return discogsResp{}, fmt.Errorf("image download returned status %d", imageResp.StatusCode)
	}

	rawImageData, err := io.ReadAll(imageResp.Body)
	if err != nil {
		return discogsResp{}, fmt.Errorf("failed to read image data: %w", err)
	}

	// Extract extension from URI
	lastDot := strings.LastIndex(imageURI, ".")
	extension := ""
	if lastDot != -1 {
		extension = imageURI[lastDot+1:]
	}

	return discogsResp{
		masterID:     masterID,
		title:        masterResult.Title,
		artist:       artistStr,
		rawCoverData: rawImageData,
		extension:    extension,
		releaseYear:  masterResult.Year,
		genres:       genresStr,
		styles:       stylesStr,
	}, nil
}

func RegisterUniqueVinylQueryParams(albumTitle, artist string) (vinyl.RegisterVinylParams, error) {
	// get raw image data []byte and string image extension .png, .jpg etc
	resp, err := requestDiscogs(albumTitle, artist)
	if err != nil {
		return vinyl.RegisterVinylParams{}, err
	}
	emb, err := RequestEmbedding(resp.rawCoverData)
	if err != nil {
		return vinyl.RegisterVinylParams{}, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Convert to pointers for nullable fields
	masterID := int64(resp.masterID)
	var stylesPtr, genresPtr *string
	if resp.styles != "" {
		stylesPtr = &resp.styles
	}
	if resp.genres != "" {
		genresPtr = &resp.genres
	}

	return vinyl.RegisterVinylParams{
		VinylTitle:        resp.title,
		VinylArtist:       resp.artist,
		VinylPressingYear: int64(resp.releaseYear), // default to initial pressing
		FirstPressing:     1,                       // default to true for now for 'master' release this will be tracked via discogs in the future
		DiscogsMasterID:   &masterID,
		Styles:            stylesPtr,
		Genres:            genresPtr,
		CoverRawBlob:      resp.rawCoverData,
		CoverEmbedding:    EmbeddingToBlob(emb),
		ImageExtension:    resp.extension,
	}, nil

}

func (k *keeper) KeepRecord(vinylID int64) error {
	if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	k.queries.RecordVinylCollection(k.ctx, vinyl.RecordVinylCollectionParams{
		UserID:  k.userID,
		VinylID: vinylID,
	})
	return nil
}

// KeepRecord makes an entry given the VinylID to the user's collection
func (k *keeper) PlayRecord(vinylID int64) error {
	if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	k.queries.PlayVinylCollection(k.ctx, vinyl.PlayVinylCollectionParams{
		VinylID: vinylID, UserID: k.userID,
	})
	k.mu.Lock()
	defer k.mu.Unlock()
	k.numPlays[vinylID] = k.numPlays[vinylID] + 1
	return nil
}

func (k *keeper) NumPlays(vinylID int64) int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.numPlays[vinylID]
}

func (k *keeper) DeleteVinyl(vinylID int64) error {
	// Delete from database
	if err := k.queries.DeleteVinyl(k.ctx, vinylID); err != nil {
		return fmt.Errorf("failed to delete vinyl %d from database: %w", vinylID, err)
	}

	// Remove from in-memory caches with proper locking
	k.mu.Lock()
	delete(k.vinylLookup, vinylID)
	delete(k.embeddingLookup, vinylID)
	delete(k.numPlays, vinylID)
	k.mu.Unlock()

	return nil
}

func (k *keeper) initKeeper(ctx context.Context) error {
	k.ctx = ctx
	// Initialize DB and queries
	if err := k.initializeQueries(ctx); err != nil {
		return err
	}
	// Ensure default user exists
	if err := k.ensureDefaultUser(); err != nil {
		return err
	}
	vinyls, err := k.queries.GetAllVinyls(k.ctx)
	if err != nil {
		return err
	}
	k.vinylLookup = make(map[int64]vinyl.VinylUnique)
	k.embeddingLookup = make(map[int64]Embedding)
	for _, v := range vinyls {
		k.vinylLookup[v.VinylID] = v
		// Decode embedding from blob
		emb, err := EmbeddingFromBlob(v.CoverEmbedding)
		if err != nil {
			return fmt.Errorf("failed to decode embedding for vinyl %d: %w", v.VinylID, err)
		}
		k.embeddingLookup[v.VinylID] = emb
	}
	userPlays, err := k.queries.GetUserVinylPlays(k.ctx, k.userID) // userID assumed 0
	if err != nil {
		return err
	}
	k.numPlays = make(map[int64]int)
	for _, p := range userPlays {
		k.numPlays[p.VinylID] = int(p.Plays)
	}
	return nil
}

const dbFileName = "vinylkeeper.db"

// initializeQueries creates or loads the DB and assigns to k.queries
func (k *keeper) initializeQueries(ctx context.Context) error {
	_, err := os.Stat(dbFileName)
	isNew := errors.Is(err, fs.ErrNotExist)
	db, err := sql.Open("sqlite", dbFileName)
	if err != nil {
		return fmt.Errorf("open sqlite %q: %w", dbFileName, err)
	}
	if isNew {
		if _, err = db.ExecContext(ctx, schemaSQL); err != nil {
			db.Close()
			return fmt.Errorf("apply schema: %w", err)
		}
	}
	queries, err := vinyl.Prepare(ctx, db)
	if err != nil {
		db.Close()
		return fmt.Errorf("prepare queries: %w", err)
	}
	k.queries = queries
	return nil
}
func (k *keeper) checkIfExists(vinylID int64) bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	_, exists := k.vinylLookup[vinylID]
	if exists {
		return true
	}
	return false
}

// ensureDefaultUser ensures that a user with name "User" exists in the database
// and sets k.userID to the user's ID
func (k *keeper) ensureDefaultUser() error {
	user, err := k.queries.CreateUser(k.ctx, "User")
	if err != nil {
		// Check if error is due to UNIQUE constraint (user already exists)
		if liteErr, ok := err.(*sqlite.Error); ok {
			code := liteErr.Code()
			if code == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
				// User already exists, we need to look up their ID
				// For now, we'll assume userID = 1 since this is the first/only user
				k.userID = 1
				return nil
			}
		}
		return fmt.Errorf("create default user: %w", err)
	}
	k.userID = user.UserID
	return nil
}

func cosineSimilarity(a, b Embedding) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// FindClosestVinyl finds the vinyl that is closest to the input embedding
// input embedding is usually going to be from the user's image
func (k *keeper) FindClosestVinyl(input Embedding) vinyl.VinylUnique {
	k.mu.RLock()
	defer k.mu.RUnlock()

	var bestVinylID int64
	maxSimilarity := -1.0
	for vID, embedding := range k.embeddingLookup {
		similarity := cosineSimilarity(input, embedding)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
			bestVinylID = vID
		}
	}

	return k.vinylLookup[bestVinylID]
}
