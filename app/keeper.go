package main

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ninesl/vinyl-keeper/app/vinyl"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const (
	discogsMinRequestInterval     = 5 * time.Second
	discogsFailureRequestInterval = 15 * time.Second
)

var (
	discogsRequestGateMu sync.Mutex
	discogsNextAllowedAt time.Time
	discogsRequestDelay  = discogsMinRequestInterval
)

func waitForDiscogsRequestSlot() {
	discogsRequestGateMu.Lock()
	defer discogsRequestGateMu.Unlock()

	now := time.Now()
	if now.Before(discogsNextAllowedAt) {
		wait := time.Until(discogsNextAllowedAt)
		log.Printf("[Discogs] rate-limit wait=%s", wait.Round(time.Millisecond))
		time.Sleep(wait)
	}

	discogsNextAllowedAt = time.Now().Add(discogsRequestDelay)
}

func recordDiscogsRequestResult(success bool) {
	discogsRequestGateMu.Lock()
	defer discogsRequestGateMu.Unlock()

	if success {
		discogsRequestDelay = discogsMinRequestInterval
		next := time.Now().Add(discogsRequestDelay)
		if discogsNextAllowedAt.After(next) {
			discogsNextAllowedAt = next
		}
		return
	}

	discogsRequestDelay = discogsFailureRequestInterval
	discogsNextAllowedAt = time.Now().Add(discogsRequestDelay)
}

type Keeper interface {
	RegisterVinylUnique(args RegisterVinylParams) (vinyl.VinylRecord, error)
	RegisterVinylFromMaster(ctx context.Context, masterID int, userID int64) (vinyl.VinylRecord, error)
	RegisterVinylFromSearch(getMasterID func(album, artist string) (int, error)) func(context.Context, string, string, int64) (vinyl.VinylRecord, error)

	KeepRecord(vinylID, userID int64) error // makes an entry for the record, returns an error if exists already
	PlayRecord(vinylID, userID int64) error // ++ to the numPlays of the vinylID, saves the record if not already logged
	NumPlays(vinylID, userID int64) int     // Number of plays this vinylID has had for this user
	AllVinyl() []vinyl.VinylRecord
	MyVinyl(userID int64) []vinyl.VinylWithPlayData // returns all vinyl user has played, ordered by last_played DESC
	DeleteUserVinyl(vinylID, userID int64) error    // removes a record from one user's collection only
	DeleteVinyl(vinylID int64) error                // removes vinyl from DB and in-memory caches
}

type RegisterVinylParams struct {
	VinylTitle     string
	VinylArtist    string
	MasterID       *int64
	MasterYear     *int64
	Styles         *string
	Genres         *string
	ReleaseID      int64
	RecentPrice    *float64
	Country        *string
	Notes          *string
	Released       string
	MasterRelease  int64
	ResourceURI    string
	ImageExtension string
	CoverRawBlob   []byte
	CoverEmbedding []byte
}

type keeper struct {
	ctx             context.Context
	db              *sql.DB
	queries         *vinyl.Queries
	vinylLookup     map[int64]vinyl.VinylRecord
	embeddingLookup map[int64]Embedding
	// number of plays per user per vinyl: userID -> (vinylID -> playCount)
	userNumPlays map[int64]map[int64]int

	// Filter index and cached sorted slices
	vinylIndex   *vinyl.VinylIndex
	needsRebuild bool

	mu sync.RWMutex
}

func NewKeeper() (Keeper, error) {
	k := &keeper{}
	err := k.initKeeper(context.Background())
	return k, err
}

func (k *keeper) AllVinyl() []vinyl.VinylRecord {
	v, err := k.queries.GetAllVinylRecords(k.ctx)
	if err != nil {
		log.Printf("[Keeper] failed to fetch all vinyl: %v", err)
		return []vinyl.VinylRecord{}
	}

	return mapVinylRecords(v)
}

func (k *keeper) MyVinyl(userID int64) []vinyl.VinylWithPlayData {
	rows, err := k.queries.GetMyVinyl(k.ctx, userID)
	if err != nil {
		log.Printf("[Keeper] failed to fetch user vinyl for user %d: %v", userID, err)
		return []vinyl.VinylWithPlayData{}
	}

	k.mu.RLock()
	defer k.mu.RUnlock()

	result := make([]vinyl.VinylWithPlayData, 0, len(rows))
	for _, row := range rows {
		vinylUnique, ok := k.vinylLookup[row.VinylID]
		if !ok {
			log.Printf("[Keeper] data consistency warning: vinyl_id %d found in vinyl_plays but not in vinylLookup", row.VinylID)
			continue
		}

		vinylUnique.Released = row.Released
		vinylUnique.ImageExtension = row.ImageExtension
		vinylUnique.RecentPrice = float64PtrFromAny(row.LowestPrice)
		vinylUnique.VinylPressingYear = row.VinylPressingYear

		firstPlayed := stringPtrIfNonEmpty(row.FirstPlayed)
		lastPlayed := stringPtrIfNonEmpty(row.LastPlayed)
		result = append(result, vinyl.VinylWithPlayData{
			VinylRecord:    vinylUnique,
			Plays:          row.Plays,
			FirstPlayed:    firstPlayed,
			LastPlayed:     lastPlayed,
			ReleaseID:      row.ReleaseID,
			ReleaseFormat:  row.ReleaseFormat,
			ReleaseCountry: stringPtrFromAny(row.ReleaseCountry),
			Label:          row.Label,
			CoverURI:       row.CoverUri,
			HasBlob:        row.HasBlob == 1,
			Notes:          stringPtrFromAny(row.Notes),
		})
	}

	return result
}

func (k *keeper) ListUsers() ([]vinyl.User, error) {
	users, err := k.queries.ListUsers(k.ctx)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (k *keeper) CreateUser(name string) (vinyl.User, error) {
	return k.queries.CreateUser(k.ctx, strings.TrimSpace(name))
}

func (k *keeper) GetUserByID(userID int64) (*vinyl.User, error) {
	user, err := k.queries.GetUserByID(k.ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (k *keeper) RegisterVinylUnique(args RegisterVinylParams) (vinyl.VinylRecord, error) {
	unique, err := k.queries.RegisterVinylUnique(k.ctx, vinyl.RegisterVinylUniqueParams{
		VinylTitle:  args.VinylTitle,
		VinylArtist: args.VinylArtist,
		MasterID:    args.MasterID,
		MasterYear:  args.MasterYear,
		Styles:      args.Styles,
		Genres:      args.Genres,
	})
	if err != nil {
		return vinyl.VinylRecord{}, err
	}

	_, err = k.queries.UpsertVinylRelease(k.ctx, vinyl.UpsertVinylReleaseParams{
		VinylID:          unique.VinylID,
		ReleaseID:        args.ReleaseID,
		LowestPrice:      args.RecentPrice,
		PriceLastUpdated: ptrDateToday(),
		Country:          args.Country,
		Notes:            args.Notes,
		Released:         args.Released,
		MasterRelease:    args.MasterRelease,
		ResourceUri:      args.ResourceURI,
		ImageExtension:   args.ImageExtension,
		CoverRawBlob:     args.CoverRawBlob,
		CoverEmbedding:   args.CoverEmbedding,
	})
	if err != nil {
		return vinyl.VinylRecord{}, err
	}

	row, err := k.queries.GetVinylRecordByID(k.ctx, unique.VinylID)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}
	vinylRecord := mapVinylRecord(row)

	// Decode embedding from the returned vinyl record
	emb, err := EmbeddingFromBlob(vinylRecord.CoverEmbedding)
	if err != nil {
		log.Printf("[Keeper] warning: failed to decode embedding for vinyl %d from DB row: %v", vinylRecord.VinylID, err)
		emb, err = EmbeddingFromBlob(args.CoverEmbedding)
		if err != nil {
			log.Printf("[Keeper] warning: failed to decode fallback embedding for vinyl %d: %v", vinylRecord.VinylID, err)
			emb = nil
		}
	}

	// Update in-memory caches with proper locking
	k.mu.Lock()
	k.vinylLookup[vinylRecord.VinylID] = vinylRecord
	if emb != nil {
		k.embeddingLookup[vinylRecord.VinylID] = emb
	}
	k.needsRebuild = true // Mark index for rebuild
	k.mu.Unlock()

	return vinylRecord, nil
}

func (k *keeper) RegisterVinylFromMaster(ctx context.Context, masterID int, userID int64) (vinyl.VinylRecord, error) {
	if masterID <= 0 {
		return vinyl.VinylRecord{}, fmt.Errorf("invalid master_id=%d", masterID)
	}
	if userID < 0 {
		return vinyl.VinylRecord{}, fmt.Errorf("invalid user_id=%d", userID)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	master, err := fetchDiscogsMaster(masterID, httpClient)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}
	masterPayload, err := buildMasterVinylPayload(masterID, master, httpClient)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}

	versions, err := fetchAllVinylVersions(masterID, httpClient)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}
	if len(versions) == 0 {
		return vinyl.VinylRecord{}, fmt.Errorf("master %d returned no vinyl versions", masterID)
	}

	masterID64 := int64(masterID)
	params := RegisterVinylParams{
		VinylTitle:     strings.TrimSpace(master.Title),
		VinylArtist:    discogsMasterArtistString(master),
		MasterID:       &masterID64,
		MasterYear:     int64PtrIfPositive(master.Year),
		Styles:         stringPtrIfNonEmpty(strings.Join(master.Styles, ",")),
		Genres:         stringPtrIfNonEmpty(strings.Join(master.Genres, ",")),
		ReleaseID:      0,
		Released:       masterPayload.Released,
		MasterRelease:  1,
		ResourceURI:    masterPayload.ResourceURI,
		ImageExtension: masterPayload.ImageExtension,
		CoverRawBlob:   masterPayload.RawCoverData,
		CoverEmbedding: masterPayload.CoverEmbedding,
	}

	return k.registerVinylWithVersionsAndOwnership(ctx, params, versions, userID)
}

func (k *keeper) registerVinylWithVersionsAndOwnership(ctx context.Context, args RegisterVinylParams, versions []discogsMasterVersion, userID int64) (vinyl.VinylRecord, error) {
	tx, err := k.db.BeginTx(ctx, nil)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}
	defer tx.Rollback()
	qtx := k.queries.WithTx(tx)

	unique, err := qtx.RegisterVinylUnique(ctx, vinyl.RegisterVinylUniqueParams{
		VinylTitle:  args.VinylTitle,
		VinylArtist: args.VinylArtist,
		MasterID:    args.MasterID,
		MasterYear:  args.MasterYear,
		Styles:      args.Styles,
		Genres:      args.Genres,
	})
	if err != nil {
		return vinyl.VinylRecord{}, err
	}

	for _, v := range versions {
		year := versionReleasedYear(v)
		if v.ID <= 0 {
			return vinyl.VinylRecord{}, fmt.Errorf("invalid release_id=%d", v.ID)
		}
		if err := qtx.UpsertVinylReleaseCheck(ctx, vinyl.UpsertVinylReleaseCheckParams{
			VinylID:       unique.VinylID,
			ReleaseID:     int64(v.ID),
			Label:         strings.TrimSpace(v.Label),
			Country:       strings.TrimSpace(v.Country),
			ReleaseFormat: strings.TrimSpace(v.Format),
			ReleasedYear:  int64FromAny(year),
			CoverUri:      strings.TrimSpace(v.Thumb),
		}); err != nil {
			return vinyl.VinylRecord{}, fmt.Errorf("upsert vinyl_releases_check release_id=%d: %w", v.ID, err)
		}
	}

	if _, err := qtx.UpsertVinylRelease(ctx, vinyl.UpsertVinylReleaseParams{
		VinylID:          unique.VinylID,
		ReleaseID:        0,
		LowestPrice:      args.RecentPrice,
		PriceLastUpdated: ptrDateToday(),
		Country:          args.Country,
		Notes:            args.Notes,
		Released:         args.Released,
		MasterRelease:    1,
		ResourceUri:      args.ResourceURI,
		ImageExtension:   args.ImageExtension,
		CoverRawBlob:     args.CoverRawBlob,
		CoverEmbedding:   args.CoverEmbedding,
	}); err != nil {
		return vinyl.VinylRecord{}, fmt.Errorf("upsert primary vinyl_release: %w", err)
	}

	if err := qtx.EnsureOwnershipPlay(ctx, vinyl.EnsureOwnershipPlayParams{UserID: userID, VinylID: unique.VinylID, ReleaseID: 0, PlayedDate: time.Now().Format("2006-01-02")}); err != nil {
		return vinyl.VinylRecord{}, fmt.Errorf("insert ownership play: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return vinyl.VinylRecord{}, err
	}

	row, err := k.queries.GetVinylRecordByID(ctx, unique.VinylID)
	if err != nil {
		return vinyl.VinylRecord{}, err
	}
	record := mapVinylRecord(row)

	emb, err := EmbeddingFromBlob(record.CoverEmbedding)
	if err != nil {
		log.Printf("[Keeper] warning: failed to decode embedding for vinyl %d after register: %v", record.VinylID, err)
	}

	k.mu.Lock()
	k.vinylLookup[record.VinylID] = record
	if err == nil {
		k.embeddingLookup[record.VinylID] = emb
	}
	if k.userNumPlays[userID] == nil {
		k.userNumPlays[userID] = make(map[int64]int)
	}
	if _, ok := k.userNumPlays[userID][record.VinylID]; !ok {
		k.userNumPlays[userID][record.VinylID] = 0
	}
	k.needsRebuild = true
	k.mu.Unlock()

	return record, nil
}

func (k *keeper) RegisterVinylFromSearch(getMasterID func(album, artist string) (int, error)) func(context.Context, string, string, int64) (vinyl.VinylRecord, error) {
	return func(ctx context.Context, artist, album string, userID int64) (vinyl.VinylRecord, error) {
		masterID, err := getMasterID(album, artist)
		if err != nil {
			return vinyl.VinylRecord{}, err
		}
		return k.RegisterVinylFromMaster(ctx, masterID, userID)
	}
}

func (k *keeper) GetPrimaryReleaseID(vinylID int64) (int64, error) {
	return k.queries.GetPrimaryReleaseID(k.ctx, vinylID)
}

func (k *keeper) FindExistingVinyl(artist, album string, masterID *int64) *vinyl.VinylRecord {
	if masterID != nil {
		v, err := k.findExistingVinylByMasterID(*masterID)
		if err != nil {
			log.Printf("[Keeper] find existing vinyl by master_id=%d failed: %v", *masterID, err)
			return nil
		}
		if v != nil {
			return v
		}
	}

	rows := k.AllVinyl()
	if masterID != nil {
		for i := range rows {
			if rows[i].MasterID != nil && *rows[i].MasterID == *masterID {
				v := rows[i]
				return &v
			}
		}
	}

	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if artist == "" || album == "" {
		return nil
	}

	for i := range rows {
		if strings.EqualFold(strings.TrimSpace(rows[i].VinylArtist), artist) && strings.EqualFold(strings.TrimSpace(rows[i].VinylTitle), album) {
			v := rows[i]
			return &v
		}
	}

	return nil
}

func (k *keeper) findExistingVinylByMasterID(masterID int64) (*vinyl.VinylRecord, error) {
	vinylID, err := k.queries.FindExistingVinylIDByMasterID(k.ctx, &masterID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	v := k.GetVinyl(vinylID)
	if v == nil {
		return nil, nil
	}
	return v, nil
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

type discogsResp struct {
	masterID      int
	releaseID     int
	title, artist string
	rawCoverData  []byte
	extension     string
	releaseYear   int
	genres        string
	styles        string
}

type discogsSearchResp struct {
	Results []struct {
		Type     string `json:"type"`
		MasterID int    `json:"master_id"`
	} `json:"results"`
}

type discogsMasterResp struct {
	Title       string   `json:"title"`
	Year        int      `json:"year"`
	MainRelease int      `json:"main_release"`
	Genres      []string `json:"genres"`
	Styles      []string `json:"styles"`
	Artists     []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Images []struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"images"`
}

// this gets the first master release from the input string
// will only really work for an album and not 45s, live albums (without saying very specific terms)
// eventually we will need multiple []discogsResp of the releases
// we can then maybe determine pressing, etc off that? Not there yet regardless
func resolveDiscogsMasterIDs(albumTitle, artist string, httpClient *http.Client) ([]int, error) {
	albumTitle = strings.TrimSpace(albumTitle)
	artist = strings.TrimSpace(artist)
	if albumTitle == "" || artist == "" {
		return nil, fmt.Errorf("must provide strings for both albumTitle and artist")
	}

	format := "&format=album"
	// Step 1: Search for the release
	searchURL := fmt.Sprintf("https://api.discogs.com/database/search?release_title=%s&artist=%s%s&per_page=1&page=1",
		strings.ReplaceAll(albumTitle, " ", "%20"),
		strings.ReplaceAll(artist, " ", "%20"),
		format)

	log.Printf("[Discogs] GET %s", searchURL)
	searchResp, err := httpClient.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("search request failed: %w", err)
	}
	defer searchResp.Body.Close()

	if searchResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(searchResp.Body)
		return nil, fmt.Errorf("search returned status %d: %s", searchResp.StatusCode, string(body))
	}

	var searchResult discogsSearchResp
	if err := json.NewDecoder(searchResp.Body).Decode(&searchResult); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	if len(searchResult.Results) == 0 {
		return nil, fmt.Errorf("no results found for album '%s' by artist '%s'", albumTitle, artist)
	}

	masterIDs := make([]int, 0, len(searchResult.Results))
	seenMasterID := make(map[int]struct{}, len(searchResult.Results))
	appendMasterID := func(masterID int) {
		if masterID == 0 {
			return
		}
		if _, exists := seenMasterID[masterID]; exists {
			return
		}
		seenMasterID[masterID] = struct{}{}
		masterIDs = append(masterIDs, masterID)
	}

	// Prefer explicit master results, then any result carrying a master_id.
	for _, result := range searchResult.Results {
		if result.Type == "master" {
			appendMasterID(result.MasterID)
		}
	}
	for _, result := range searchResult.Results {
		appendMasterID(result.MasterID)
	}

	if len(masterIDs) == 0 {
		return nil, fmt.Errorf("no master_id found in search results for album '%s' by artist '%s'", albumTitle, artist)
	}

	return masterIDs, nil
}

func FindDiscogsMasterID(albumTitle, artist string) (int, error) {
	httpClient := &http.Client{Timeout: time.Second * 10}
	masterIDs, err := resolveDiscogsMasterIDs(albumTitle, artist, httpClient)
	if err != nil {
		return 0, err
	}
	return masterIDs[0], nil
}

func requestDiscogs(albumTitle, artist string) (discogsResp, error) {
	httpClient := &http.Client{Timeout: time.Second * 10}
	masterIDs, err := resolveDiscogsMasterIDs(albumTitle, artist, httpClient)
	if err != nil {
		return discogsResp{}, err
	}

	var lastErr error
	for _, masterID := range masterIDs {
		resp, err := requestMasterDiscogs(masterID)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return discogsResp{}, fmt.Errorf("failed to resolve usable discogs master from search results: %w", lastErr)
	}

	return discogsResp{}, fmt.Errorf("failed to resolve usable discogs master from search results")
}

func requestMasterDiscogs(masterID int) (discogsResp, error) {
	httpClient := http.Client{
		Timeout: time.Second * 10,
	}
	// Step 2: Get master release details
	masterURL := fmt.Sprintf("https://api.discogs.com/masters/%d", masterID)
	log.Printf("[Discogs] GET %s", masterURL)
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

	// Find primary image URI, then fall back to the first available image.
	var imageURI string
	for i := 0; i < len(masterResult.Images); i++ {
		if masterResult.Images[i].Type == "primary" {
			imageURI = masterResult.Images[i].URI
			break
		}
	}
	if imageURI == "" && len(masterResult.Images) > 0 {
		imageURI = masterResult.Images[0].URI
	}

	if imageURI == "" {
		return discogsResp{}, fmt.Errorf("no image found for master %d", masterID)
	}

	// Step 3: Download the image
	log.Printf("[Discogs] GET %s", imageURI)
	waitForDiscogsRequestSlot()
	imageResp, err := httpClient.Get(imageURI)
	if err != nil {
		recordDiscogsRequestResult(false)
		return discogsResp{}, fmt.Errorf("image download failed: %w", err)
	}
	defer imageResp.Body.Close()

	if imageResp.StatusCode != http.StatusOK {
		recordDiscogsRequestResult(false)
		return discogsResp{}, fmt.Errorf("image download returned status %d", imageResp.StatusCode)
	}
	recordDiscogsRequestResult(true)

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
		releaseID:    masterResult.MainRelease,
		title:        masterResult.Title,
		artist:       artistStr,
		rawCoverData: rawImageData,
		extension:    extension,
		releaseYear:  masterResult.Year,
		genres:       genresStr,
		styles:       stylesStr,
	}, nil
}

func discogsMasterArtistString(master discogsMasterResp) string {
	artistNames := make([]string, 0, len(master.Artists))
	for _, a := range master.Artists {
		name := strings.TrimSpace(a.Name)
		if name != "" {
			artistNames = append(artistNames, name)
		}
	}
	return strings.Join(artistNames, ",")
}

func buildMasterVinylPayload(masterID int, master discogsMasterResp, httpClient *http.Client) (mainReleaseBackfill, error) {
	imageURI := pickDiscogsImageURI(master.Images)
	if imageURI == "" {
		return mainReleaseBackfill{}, fmt.Errorf("no image URI found for master %d", masterID)
	}

	rawImageData, err := downloadDiscogsImage(imageURI, httpClient)
	if err != nil {
		return mainReleaseBackfill{}, err
	}

	embedding, err := RequestEmbedding(rawImageData)
	if err != nil {
		return mainReleaseBackfill{}, fmt.Errorf("generate embedding for master %d: %w", masterID, err)
	}

	return mainReleaseBackfill{
		ReleaseID:      master.MainRelease,
		Released:       strconv.Itoa(master.Year),
		ResourceURI:    fmt.Sprintf("https://api.discogs.com/masters/%d", masterID),
		ImageExtension: imageExtensionFromURI(imageURI),
		RawCoverData:   rawImageData,
		CoverEmbedding: EmbeddingToBlob(embedding),
	}, nil
}

func RegisterUniqueVinylMasterID(masterID int) (RegisterVinylParams, error) {
	resp, err := requestMasterDiscogs(masterID)
	if err != nil {
		return RegisterVinylParams{}, err
	}
	return registerParams(resp)
}

func RegisterUniqueVinylAlbumArtist(albumTitle, artist string) (RegisterVinylParams, error) {
	// get raw image data []byte and string image extension .png, .jpg etc
	resp, err := requestDiscogs(albumTitle, artist)
	if err != nil {
		return RegisterVinylParams{}, err
	}
	return registerParams(resp)
}

func registerParams(resp discogsResp) (RegisterVinylParams, error) {
	emb, err := RequestEmbedding(resp.rawCoverData)
	if err != nil {
		return RegisterVinylParams{}, fmt.Errorf("failed to generate embedding: %w", err)
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

	releaseID := int64(resp.releaseID)
	if releaseID == 0 {
		releaseID = int64(resp.masterID)
	}

	return RegisterVinylParams{
		VinylTitle:     resp.title,
		VinylArtist:    resp.artist,
		MasterID:       &masterID,
		MasterYear:     int64PtrIfPositive(resp.releaseYear),
		Styles:         stylesPtr,
		Genres:         genresPtr,
		ReleaseID:      releaseID,
		Released:       strconv.Itoa(resp.releaseYear),
		MasterRelease:  1,
		ResourceURI:    fmt.Sprintf("https://api.discogs.com/masters/%d", masterID),
		ImageExtension: resp.extension,
		CoverRawBlob:   resp.rawCoverData,
		CoverEmbedding: EmbeddingToBlob(emb),
	}, nil
}

func (k *keeper) KeepRecord(vinylID, userID int64) error {
	if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	if err := k.queries.EnsureOwnershipPlay(k.ctx, vinyl.EnsureOwnershipPlayParams{
		UserID:     userID,
		VinylID:    vinylID,
		ReleaseID:  0,
		PlayedDate: time.Now().Format("2006-01-02"),
	}); err != nil {
		return fmt.Errorf("failed to record ownership for vinyl %d user %d: %w", vinylID, userID, err)
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if k.userNumPlays[userID] == nil {
		k.userNumPlays[userID] = make(map[int64]int)
	}
	if _, ok := k.userNumPlays[userID][vinylID]; !ok {
		k.userNumPlays[userID][vinylID] = 0
	}
	return nil
}

// PlayRecord makes an entry given the VinylID to the user's collection
func (k *keeper) PlayRecord(vinylID, userID int64) error {
	if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	releaseID, err := k.currentReleaseID(vinylID, userID)
	if err != nil {
		return fmt.Errorf("failed to resolve current release for vinyl %d: %w", vinylID, err)
	}
	if err := k.queries.EnsureOwnershipPlay(k.ctx, vinyl.EnsureOwnershipPlayParams{
		UserID:     userID,
		VinylID:    vinylID,
		ReleaseID:  releaseID,
		PlayedDate: time.Now().Format("2006-01-02"),
	}); err != nil {
		return fmt.Errorf("failed to ensure ownership play for vinyl %d: %w", vinylID, err)
	}

	nextPlay, err := k.queries.NextPlayNumber(k.ctx, vinyl.NextPlayNumberParams{
		UserID:    userID,
		VinylID:   vinylID,
		ReleaseID: releaseID,
	})
	if err != nil {
		return fmt.Errorf("failed to resolve next play number for vinyl %d: %w", vinylID, err)
	}

	if err := k.queries.InsertVinylPlay(k.ctx, vinyl.InsertVinylPlayParams{
		UserID:     userID,
		VinylID:    vinylID,
		ReleaseID:  releaseID,
		Play:       nextPlay,
		PlayedDate: time.Now().Format("2006-01-02"),
	}); err != nil {
		return fmt.Errorf("failed to record play for vinyl %d: %w", vinylID, err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.userNumPlays[userID] == nil {
		k.userNumPlays[userID] = make(map[int64]int)
	}
	k.userNumPlays[userID][vinylID] = int(nextPlay)
	return nil
}

func (k *keeper) PlayRecordRelease(vinylID, releaseID, userID int64) error {
	if releaseID < 0 {
		return fmt.Errorf("invalid release_id=%d", releaseID)
	}
	if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	if releaseID > 0 {
		if err := k.ensureReleaseMaterialized(vinylID, releaseID); err != nil {
			return fmt.Errorf("materialize release %d for vinyl %d: %w", releaseID, vinylID, err)
		}
	}

	if err := k.queries.EnsureOwnershipPlay(k.ctx, vinyl.EnsureOwnershipPlayParams{
		UserID:     userID,
		VinylID:    vinylID,
		ReleaseID:  releaseID,
		PlayedDate: time.Now().Format("2006-01-02"),
	}); err != nil {
		return fmt.Errorf("failed to ensure ownership play for vinyl %d release %d: %w", vinylID, releaseID, err)
	}

	nextPlay, err := k.queries.NextPlayNumber(k.ctx, vinyl.NextPlayNumberParams{
		UserID:    userID,
		VinylID:   vinylID,
		ReleaseID: releaseID,
	})
	if err != nil {
		return fmt.Errorf("failed to resolve next play number for vinyl %d release %d: %w", vinylID, releaseID, err)
	}

	if err := k.queries.InsertVinylPlay(k.ctx, vinyl.InsertVinylPlayParams{
		UserID:     userID,
		VinylID:    vinylID,
		ReleaseID:  releaseID,
		Play:       nextPlay,
		PlayedDate: time.Now().Format("2006-01-02"),
	}); err != nil {
		return fmt.Errorf("failed to record play for vinyl %d release %d: %w", vinylID, releaseID, err)
	}

	k.mu.Lock()
	defer k.mu.Unlock()
	if k.userNumPlays[userID] == nil {
		k.userNumPlays[userID] = make(map[int64]int)
	}
	k.userNumPlays[userID][vinylID] = int(nextPlay)
	return nil
}

func (k *keeper) currentReleaseID(vinylID, userID int64) (int64, error) {
	releaseID, err := k.queries.GetCurrentReleaseID(k.ctx, vinyl.GetCurrentReleaseIDParams{UserID: userID, VinylID: vinylID})
	if err == nil {
		return releaseID, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return 0, err
}

func (k *keeper) FindClosestReleaseCandidates(input Embedding, n int, userID int64) []vinyl.ReleaseCandidate {
	if n <= 0 {
		return []vinyl.ReleaseCandidate{}
	}
	rows, err := k.queries.ListUserScanRows(k.ctx, userID)
	if err != nil {
		log.Printf("[Keeper] failed to query release candidates: %v", err)
		return []vinyl.ReleaseCandidate{}
	}

	type scoredCandidate struct {
		candidate vinyl.ReleaseCandidate
		score     float64
	}
	scored := make([]scoredCandidate, 0)

	for _, row := range rows {
		emb, err := EmbeddingFromBlob(row.CoverEmbedding)
		if err != nil {
			continue
		}

		sim := cosineSimilarity(input, emb)
		scored = append(scored, scoredCandidate{
			candidate: vinyl.ReleaseCandidate{
				VinylRecord: vinyl.VinylRecord{
					VinylID:           row.VinylID,
					VinylTitle:        row.VinylTitle,
					VinylArtist:       row.VinylArtist,
					MasterID:          row.MasterID,
					Genres:            row.Genres,
					Styles:            row.Styles,
					Country:           stringPtrFromAny(row.Country),
					Released:          row.Released,
					RecentPrice:       row.LowestPrice,
					ImageExtension:    row.ImageExtension,
					CoverEmbedding:    row.CoverEmbedding,
					VinylPressingYear: row.VinylPressingYear,
				},
				ReleaseID:      row.ReleaseID,
				Label:          stringPtrFromAny(row.Label),
				ReleaseFormat:  stringPtrFromAny(row.ReleaseFormat),
				ReleaseCountry: stringPtrFromAny(row.ReleaseCountry),
				CoverURI:       stringPtrFromAny(row.CoverUri),
				HasBlob:        row.HasBlob == 1,
				Notes:          stringPtrFromAny(row.Notes),
				Similarity:     sim,
			},
			score: sim,
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			if scored[i].candidate.VinylID == scored[j].candidate.VinylID {
				return scored[i].candidate.ReleaseID < scored[j].candidate.ReleaseID
			}
			return scored[i].candidate.VinylID < scored[j].candidate.VinylID
		}
		return scored[i].score > scored[j].score
	})

	if n > len(scored) {
		n = len(scored)
	}
	out := make([]vinyl.ReleaseCandidate, 0, n)
	for i := 0; i < n; i++ {
		candidate := scored[i].candidate
		candidate.Similarity = scored[i].score
		out = append(out, candidate)
	}
	return out
}

func (k *keeper) UserOwnsMaster(userID, masterID int64) bool {
	if userID < 0 || masterID <= 0 {
		return false
	}
	owns, err := k.queries.UserOwnsMaster(k.ctx, vinyl.UserOwnsMasterParams{
		UserID:   userID,
		MasterID: &masterID,
	})
	if err != nil {
		log.Printf("[Keeper] failed checking owned master user_id=%d master_id=%d: %v", userID, masterID, err)
		return false
	}
	return owns != 0
}

func (k *keeper) GetReleaseCandidate(vinylID, releaseID int64) (*vinyl.ReleaseCandidate, error) {
	scanned, err := k.queries.GetReleaseCandidateRow(k.ctx, vinyl.GetReleaseCandidateRowParams{VinylID: vinylID, ReleaseID: releaseID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}

	return &vinyl.ReleaseCandidate{
		VinylRecord: vinyl.VinylRecord{
			VinylID:           scanned.VinylID,
			VinylTitle:        scanned.VinylTitle,
			VinylArtist:       scanned.VinylArtist,
			MasterID:          scanned.MasterID,
			Genres:            scanned.Genres,
			Styles:            scanned.Styles,
			Country:           scanned.Country,
			Released:          scanned.Released,
			RecentPrice:       scanned.LowestPrice,
			ImageExtension:    scanned.ImageExtension,
			CoverEmbedding:    scanned.CoverEmbedding,
			VinylPressingYear: scanned.VinylPressingYear,
		},
		ReleaseID:      scanned.ReleaseID,
		Label:          scanned.Label,
		ReleaseFormat:  scanned.ReleaseFormat,
		ReleaseCountry: scanned.ReleaseCountry,
		CoverURI:       scanned.CoverUri,
		HasBlob:        scanned.HasBlob == 1,
		Notes:          scanned.Notes,
	}, nil
}

func (k *keeper) ListPressingOptions(vinylID, userID int64) ([]vinyl.ReleaseOption, error) {
	rows, err := k.queries.ListPressingOptionRows(k.ctx, vinyl.ListPressingOptionRowsParams{UserID: userID, VinylID: vinylID, VinylID_2: vinylID})
	if err != nil {
		return nil, err
	}

	base := k.GetVinyl(vinylID)
	result := make([]vinyl.ReleaseOption, 0, len(rows)+1)
	currentReleaseID, currentErr := k.currentReleaseID(vinylID, userID)
	if currentErr != nil {
		log.Printf("[Keeper] failed resolving current release for options vinyl_id=%d user_id=%d: %v", vinylID, userID, currentErr)
	}
	if base != nil {
		masterLabel := "Master"
		result = append(result, vinyl.ReleaseOption{
			ReleaseCandidate: vinyl.ReleaseCandidate{
				VinylRecord:    *base,
				ReleaseID:      0,
				Label:          &masterLabel,
				ReleaseFormat:  nil,
				ReleaseCountry: nil,
				CoverURI:       nil,
				HasBlob:        true,
				Notes:          nil,
				Similarity:     0,
			},
			IsCurrent: currentErr == nil && currentReleaseID == 0,
		})
	}
	for _, row := range rows {
		result = append(result, vinyl.ReleaseOption{
			ReleaseCandidate: vinyl.ReleaseCandidate{
				VinylRecord: vinyl.VinylRecord{
					VinylID:           row.VinylID,
					VinylTitle:        row.VinylTitle,
					VinylArtist:       row.VinylArtist,
					MasterID:          row.MasterID,
					Genres:            row.Genres,
					Styles:            row.Styles,
					Country:           stringPtrFromAny(row.Country),
					Released:          row.Released,
					RecentPrice:       row.LowestPrice,
					ImageExtension:    row.ImageExtension,
					VinylPressingYear: row.VinylPressingYear,
				},
				ReleaseID:      row.ReleaseID,
				Label:          stringPtrFromAny(row.Label),
				ReleaseFormat:  stringPtrFromAny(row.ReleaseFormat),
				ReleaseCountry: stringPtrFromAny(row.ReleaseCountry),
				CoverURI:       stringPtrFromAny(row.CoverUri),
				HasBlob:        row.HasBlob == 1,
				Notes:          row.Notes,
			},
			IsCurrent: row.IsCurrent == 1,
		})
	}

	return result, nil
}

func (k *keeper) ensureReleaseMaterialized(vinylID, releaseID int64) error {
	lengths, err := k.queries.GetReleaseBlobLengths(k.ctx, vinyl.GetReleaseBlobLengthsParams{VinylID: vinylID, ReleaseID: releaseID})
	if err == nil && lengths.CoverEmbeddingLen != nil && lengths.CoverRawBlobLen != nil && *lengths.CoverEmbeddingLen > 0 && *lengths.CoverRawBlobLen > 0 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	v := k.GetVinyl(vinylID)
	if v == nil {
		return fmt.Errorf("vinyl %d not found", vinylID)
	}

	masterID := 0
	if v.MasterID != nil {
		masterID = int(*v.MasterID)
	}
	httpClient := &http.Client{Timeout: 10 * time.Second}
	payload, _, err := buildMainReleasePayload(masterID, int(releaseID), httpClient)
	if err != nil {
		return err
	}

	currentMaster, err := k.queries.GetReleaseMasterFlag(k.ctx, vinyl.GetReleaseMasterFlagParams{VinylID: vinylID, ReleaseID: releaseID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		currentMaster = 0
	}

	_, err = k.queries.UpsertVinylRelease(k.ctx, vinyl.UpsertVinylReleaseParams{
		VinylID:          vinylID,
		ReleaseID:        releaseID,
		LowestPrice:      payload.LowestPrice,
		PriceLastUpdated: ptrDateToday(),
		Country:          stringPtrIfNonEmpty(payload.Country),
		Notes:            payload.Notes,
		Released:         payload.Released,
		MasterRelease:    currentMaster,
		ResourceUri:      payload.ResourceURI,
		ImageExtension:   payload.ImageExtension,
		CoverRawBlob:     payload.RawCoverData,
		CoverEmbedding:   payload.CoverEmbedding,
	})
	if err != nil {
		return err
	}

	if err := k.reloadVinylCaches(); err != nil {
		return err
	}
	return nil
}

func (k *keeper) ChangeUserPressing(vinylID, releaseID, userID int64) error {
	if releaseID < 0 {
		return fmt.Errorf("invalid release_id=%d", releaseID)
	}
	if releaseID > 0 {
		if err := k.ensureReleaseMaterialized(vinylID, releaseID); err != nil {
			return fmt.Errorf("materialize release %d for vinyl %d: %w", releaseID, vinylID, err)
		}
	} else if !k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}

	tx, err := k.db.BeginTx(k.ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	qtx := k.queries.WithTx(tx)
	rows, err := qtx.ListUserVinylPlayDates(k.ctx, vinyl.ListUserVinylPlayDatesParams{UserID: userID, VinylID: vinylID})
	if err != nil {
		return err
	}

	ownershipDate := ""
	playDates := make([]string, 0)
	for _, row := range rows {
		if row.Play == 0 {
			if ownershipDate == "" || row.PlayedDate < ownershipDate {
				ownershipDate = row.PlayedDate
			}
			continue
		}
		playDates = append(playDates, row.PlayedDate)
	}

	if ownershipDate == "" {
		if len(playDates) > 0 {
			ownershipDate = playDates[0]
		} else {
			ownershipDate = time.Now().Format("2006-01-02")
		}
	}

	if err := qtx.DeleteUserVinylPlays(k.ctx, vinyl.DeleteUserVinylPlaysParams{UserID: userID, VinylID: vinylID}); err != nil {
		return err
	}

	if err := qtx.EnsureOwnershipPlay(k.ctx, vinyl.EnsureOwnershipPlayParams{UserID: userID, VinylID: vinylID, ReleaseID: releaseID, PlayedDate: ownershipDate}); err != nil {
		return err
	}

	for i, playedDate := range playDates {
		if err := qtx.InsertVinylPlay(k.ctx, vinyl.InsertVinylPlayParams{UserID: userID, VinylID: vinylID, ReleaseID: releaseID, Play: int64(i + 1), PlayedDate: playedDate}); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	k.mu.Lock()
	if k.userNumPlays[userID] == nil {
		k.userNumPlays[userID] = make(map[int64]int)
	}
	k.userNumPlays[userID][vinylID] = len(playDates)
	k.mu.Unlock()

	return nil
}

func (k *keeper) NumPlays(vinylID, userID int64) int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.userNumPlays[userID] == nil {
		return 0
	}
	return k.userNumPlays[userID][vinylID]
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
	// Remove from all users' play counts
	for userID := range k.userNumPlays {
		delete(k.userNumPlays[userID], vinylID)
	}
	k.needsRebuild = true // Mark index for rebuild
	k.mu.Unlock()

	return nil
}

func (k *keeper) DeleteUserVinyl(vinylID, userID int64) error {
	if err := k.queries.DeleteUserVinylPlays(k.ctx, vinyl.DeleteUserVinylPlaysParams{UserID: userID, VinylID: vinylID}); err != nil {
		return fmt.Errorf("failed to remove vinyl %d from user %d collection: %w", vinylID, userID, err)
	}

	k.mu.Lock()
	if k.userNumPlays[userID] != nil {
		delete(k.userNumPlays[userID], vinylID)
	}
	k.mu.Unlock()

	return nil
}

func (k *keeper) DeleteUser(userID int64) error {
	if err := k.queries.DeleteUser(k.ctx, userID); err != nil {
		return fmt.Errorf("failed to delete user %d from database: %w", userID, err)
	}

	k.mu.Lock()
	delete(k.userNumPlays, userID)
	k.mu.Unlock()

	return nil
}

func (k *keeper) initKeeper(ctx context.Context) error {
	k.ctx = ctx
	// Initialize DB and queries
	if err := k.initializeQueries(ctx); err != nil {
		return err
	}

	if err := k.reloadVinylCaches(); err != nil {
		return err
	}

	// Load ALL user plays from all users
	allUserPlays, err := k.queries.GetAllUserVinylPlays(k.ctx)
	if err != nil {
		return err
	}
	k.userNumPlays = make(map[int64]map[int64]int)
	for _, play := range allUserPlays {
		if k.userNumPlays[play.UserID] == nil {
			k.userNumPlays[play.UserID] = make(map[int64]int)
		}
		k.userNumPlays[play.UserID][play.VinylID] = int(play.Plays)
	}

	// Build initial vinyl index
	k.rebuildIndex()

	return nil
}

func (k *keeper) reloadVinylCaches() error {
	vinyls, err := k.queries.GetAllVinylRecords(k.ctx)
	if err != nil {
		return err
	}

	lookup := make(map[int64]vinyl.VinylRecord, len(vinyls))
	embeddings := make(map[int64]Embedding, len(vinyls))
	for _, row := range vinyls {
		v := mapVinylRecord(row)
		lookup[v.VinylID] = v
		emb, err := EmbeddingFromBlob(v.CoverEmbedding)
		if err != nil {
			log.Printf("[Keeper] warning: skipping vinyl %d scan cache embedding: %v", v.VinylID, err)
			continue
		}
		embeddings[v.VinylID] = emb
	}

	k.mu.Lock()
	k.vinylLookup = lookup
	k.embeddingLookup = embeddings
	k.needsRebuild = true
	k.mu.Unlock()

	return nil
}

const dbFileName = "vinylkeeper.db"

func sqlitePoolSetting(envKey string) (int, error) {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return 0, fmt.Errorf("%s is required", envKey)
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", envKey, err)
	}
	if value < 1 {
		return 0, fmt.Errorf("%s must be >= 1 (got %d)", envKey, value)
	}

	return value, nil
}

func databasePath() (string, error) {
	if path := os.Getenv("DB_PATH"); path != "" {
		return path, nil
	}

	return "", fmt.Errorf("DB_PATH is required; expected canonical database path (for example /data/%s in containers or ./data/%s locally)", dbFileName, dbFileName)
}

func mapVinylRecords(rows []vinyl.GetAllVinylRecordsRow) []vinyl.VinylRecord {
	items := make([]vinyl.VinylRecord, 0, len(rows))
	for _, row := range rows {
		items = append(items, mapVinylRecord(row))
	}
	return items
}

func mapVinylRecord(row any) vinyl.VinylRecord {
	switch r := row.(type) {
	case vinyl.GetAllVinylRecordsRow:
		return vinyl.VinylRecord{
			VinylID:           r.VinylID,
			VinylTitle:        r.VinylTitle,
			VinylArtist:       r.VinylArtist,
			VinylPressingYear: r.VinylPressingYear,
			MasterID:          r.MasterID,
			MasterYear:        r.MasterYear,
			Genres:            r.Genres,
			Styles:            r.Styles,
			Country:           r.Country,
			Released:          r.Released,
			RecentPrice:       r.RecentPrice,
			ImageExtension:    r.ImageExtension,
			CoverRawBlob:      r.CoverRawBlob,
			CoverEmbedding:    r.CoverEmbedding,
		}
	case vinyl.GetVinylRecordByIDRow:
		return vinyl.VinylRecord{
			VinylID:           r.VinylID,
			VinylTitle:        r.VinylTitle,
			VinylArtist:       r.VinylArtist,
			VinylPressingYear: r.VinylPressingYear,
			MasterID:          r.MasterID,
			MasterYear:        r.MasterYear,
			Genres:            r.Genres,
			Styles:            r.Styles,
			Country:           r.Country,
			Released:          r.Released,
			RecentPrice:       r.RecentPrice,
			ImageExtension:    r.ImageExtension,
			CoverRawBlob:      r.CoverRawBlob,
			CoverEmbedding:    r.CoverEmbedding,
		}
	default:
		return vinyl.VinylRecord{}
	}
}

func stringPtrIfNonEmpty(v string) *string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func int64PtrIfPositive(v int) *int64 {
	if v <= 0 {
		return nil
	}
	out := int64(v)
	return &out
}

func stringPtrFromAny(v any) *string {
	switch val := v.(type) {
	case nil:
		return nil
	case string:
		return stringPtrIfNonEmpty(val)
	case []byte:
		return stringPtrIfNonEmpty(string(val))
	default:
		return stringPtrIfNonEmpty(fmt.Sprint(val))
	}
}

func float64PtrFromAny(v any) *float64 {
	switch val := v.(type) {
	case nil:
		return nil
	case float64:
		return &val
	case int64:
		out := float64(val)
		return &out
	case []byte:
		parsed, err := strconv.ParseFloat(string(val), 64)
		if err != nil {
			return nil
		}
		return &parsed
	case string:
		parsed, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return nil
		}
		return &parsed
	default:
		return nil
	}
}

func int64FromAny(v any) int64 {
	switch val := v.(type) {
	case nil:
		return 0
	case int64:
		return val
	case int:
		return int64(val)
	case []byte:
		parsed, err := strconv.ParseInt(string(val), 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	case string:
		parsed, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func ptrDateToday() *string {
	v := time.Now().Format("2006-01-02")
	return &v
}

func columnExists(ctx context.Context, db *sql.DB, tableName, columnName string) (bool, error) {
	query := fmt.Sprintf("SELECT 1 FROM pragma_table_info('%s') WHERE name = ? LIMIT 1", tableName)
	var one int
	err := db.QueryRowContext(ctx, query, columnName).Scan(&one)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

type tableColumnInfo struct {
	Name    string
	NotNull bool
}

func tableColumns(ctx context.Context, db *sql.DB, tableName string) (map[string]tableColumnInfo, error) {
	query := fmt.Sprintf("SELECT name, \"notnull\" FROM pragma_table_info('%s')", tableName)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]tableColumnInfo)
	for rows.Next() {
		var info tableColumnInfo
		var notNullInt int64
		if err := rows.Scan(&info.Name, &notNullInt); err != nil {
			return nil, err
		}
		info.NotNull = notNullInt == 1
		columns[info.Name] = info
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

func columnExpr(columns map[string]tableColumnInfo, column string) string {
	if _, ok := columns[column]; ok {
		return column
	}
	return "NULL"
}

func rebuildVinylUniqueCanonical(ctx context.Context, db *sql.DB, columns map[string]tableColumnInfo) error {
	masterExpr := "NULL"
	if _, ok := columns["master_id"]; ok {
		masterExpr = "master_id"
	}
	if _, ok := columns["discogs_master_id"]; ok {
		if masterExpr == "NULL" {
			masterExpr = "discogs_master_id"
		} else {
			masterExpr = "COALESCE(master_id, discogs_master_id)"
		}
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disable foreign keys for vinyl_unique rebuild: %w", err)
	}
	defer db.ExecContext(ctx, "PRAGMA foreign_keys = ON")

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

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
		SELECT vinyl_id, vinyl_title, vinyl_artist, %s, %s, styles, genres
		FROM vinyl_unique
	`, masterExpr, columnExpr(columns, "master_year"))
	if _, err := tx.ExecContext(ctx, insertStmt); err != nil {
		return fmt.Errorf("copy vinyl_unique into canonical table: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "DROP TABLE vinyl_unique"); err != nil {
		return fmt.Errorf("drop legacy vinyl_unique: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE vinyl_unique_new RENAME TO vinyl_unique"); err != nil {
		return fmt.Errorf("rename vinyl_unique_new: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_key_check"); err != nil {
		return fmt.Errorf("foreign key check after vinyl_unique rebuild: %w", err)
	}

	return nil
}

func ensureSchemaCompatibility(ctx context.Context, db *sql.DB) error {
	columns, err := tableColumns(ctx, db, "vinyl_unique")
	if err != nil {
		return fmt.Errorf("read vinyl_unique columns: %w", err)
	}

	legacyColumns := []string{
		"discogs_master_id",
		"vinyl_pressing_year",
		"first_pressing",
	}
	rebuildNeeded := false
	for _, column := range legacyColumns {
		if _, exists := columns[column]; exists {
			rebuildNeeded = true
			break
		}
	}
	if _, hasMasterID := columns["master_id"]; !hasMasterID {
		rebuildNeeded = true
	}
	for _, column := range []string{"master_year"} {
		if _, exists := columns[column]; !exists {
			rebuildNeeded = true
			break
		}
	}

	if rebuildNeeded {
		if err := rebuildVinylUniqueCanonical(ctx, db, columns); err != nil {
			return fmt.Errorf("rebuild canonical vinyl_unique table: %w", err)
		}
	}

	return nil
}

// initializeQueries creates or loads the DB and assigns to k.queries
func (k *keeper) initializeQueries(ctx context.Context) error {
	dbPath, err := databasePath()
	if err != nil {
		return err
	}

	maxOpenSQLite, err := sqlitePoolSetting("MAX_OPEN_SQLITE")
	if err != nil {
		return err
	}
	maxIdleSQLite, err := sqlitePoolSetting("MAX_IDLE_SQLITE")
	if err != nil {
		return err
	}
	if maxIdleSQLite > maxOpenSQLite {
		return fmt.Errorf("MAX_IDLE_SQLITE (%d) cannot be greater than MAX_OPEN_SQLITE (%d)", maxIdleSQLite, maxOpenSQLite)
	}

	dir := filepath.Dir(dbPath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create db directory %q: %w", dir, err)
		}
	}

	openPath := dbPath + "?_pragma=foreign_keys(1)"
	if strings.Contains(dbPath, "?") {
		openPath = dbPath + "&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", openPath)
	if err != nil {
		return fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(maxOpenSQLite)
	db.SetMaxIdleConns(maxIdleSQLite)
	// Foreign key enforcement is configured via DSN pragma so it applies to each
	// new connection in the database/sql pool. Apply schema on each start so
	// existing databases pick up additive table changes.
	if _, err = db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return fmt.Errorf("apply schema: %w", err)
	}
	if err := ensureSchemaCompatibility(ctx, db); err != nil {
		db.Close()
		return fmt.Errorf("ensure schema compatibility: %w", err)
	}
	queries, err := vinyl.Prepare(ctx, db)
	if err != nil {
		db.Close()
		return fmt.Errorf("prepare queries: %w", err)
	}
	k.db = db
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

func (k *keeper) GetVinyl(vinylID int64) *vinyl.VinylRecord {
	k.mu.RLock()
	defer k.mu.RUnlock()
	v, exists := k.vinylLookup[vinylID]
	if !exists {
		return nil
	}
	return &v
}

func (k *keeper) GetReleaseCover(vinylID, releaseID int64) ([]byte, string, bool) {
	row, err := k.queries.GetReleaseCover(k.ctx, vinyl.GetReleaseCoverParams{VinylID: vinylID, ReleaseID: releaseID})
	if err != nil {
		return nil, "", false
	}
	if len(row.CoverRawBlob) == 0 {
		return nil, "", false
	}
	return row.CoverRawBlob, row.ImageExtension, true
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
func (k *keeper) FindClosestVinyl(input Embedding) vinyl.VinylRecord {
	vinyls := k.FindClosestVinyls(input, 1)
	if len(vinyls) == 0 {
		return vinyl.VinylRecord{}
	}
	return vinyls[0]
}

func (k *keeper) FindClosestVinyls(input Embedding, n int) []vinyl.VinylRecord {
	if n <= 0 {
		return []vinyl.VinylRecord{}
	}

	type scoredVinyl struct {
		vinylID    int64
		similarity float64
	}

	k.mu.RLock()
	defer k.mu.RUnlock()

	scored := make([]scoredVinyl, 0, len(k.embeddingLookup))
	for vID, embedding := range k.embeddingLookup {
		scored = append(scored, scoredVinyl{
			vinylID:    vID,
			similarity: cosineSimilarity(input, embedding),
		})
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].similarity == scored[j].similarity {
			return scored[i].vinylID < scored[j].vinylID
		}
		return scored[i].similarity > scored[j].similarity
	})

	if n > len(scored) {
		n = len(scored)
	}

	result := make([]vinyl.VinylRecord, 0, n)
	for i := 0; i < n; i++ {
		result = append(result, k.vinylLookup[scored[i].vinylID])
	}

	return result
}

// GetVinylIndex returns the vinyl index, rebuilding it if necessary
func (k *keeper) GetVinylIndex() *vinyl.VinylIndex {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.needsRebuild {
		k.rebuildIndexLocked()
	}

	return k.vinylIndex
}

// rebuildIndex rebuilds the vinyl index with proper locking
func (k *keeper) rebuildIndex() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rebuildIndexLocked()
}

// rebuildIndexLocked rebuilds the index (must be called with lock held)
func (k *keeper) rebuildIndexLocked() {
	vinyls := make([]vinyl.VinylRecord, 0, len(k.vinylLookup))
	for _, v := range k.vinylLookup {
		vinyls = append(vinyls, v)
	}
	k.vinylIndex = vinyl.BuildVinylIndex(vinyls)
	k.needsRebuild = false
}
