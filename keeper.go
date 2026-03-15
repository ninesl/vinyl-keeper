package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"sync"

	"github.com/ninesl/vinyl-keeper/vinyl"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type Keeper interface {
	RegisterVinyl(args vinyl.RegisterVinylParams) error // FIXME: this is not discogs? uncertain schema for this yet

	KeepRecord(vinylID int64) error // makes an entry for the record, returns an error if exists already
	PlayRecord(vinylID int64) error // ++ to the numPlays of the vinylID, saves the record if not already logged
	NumPlays(vinylID int64) int     // Number of plays this vinylID has had in this Keeper
}

type keeper struct {
	ctx         context.Context
	userID      int64
	queries     *vinyl.Queries
	vinylLookup map[int64]vinyl.VinylUnique
	// number of plays each int64 vinylID has had
	numPlays map[int64]int
	mu       *sync.RWMutex
}

func NewKeeper() (Keeper, error) {
	k := keeper{}
	err := k.initKeeper(context.Background())
	return k, err
}

func (k keeper) RegisterVinyl(args vinyl.RegisterVinylParams) error {
	_, err := k.queries.RegisterVinyl(k.ctx, args)
	return err
}

func (k keeper) KeepRecord(vinylID int64) error {
	if k.checkIfExists(vinylID) {
		return fmt.Errorf("%d does not exist in keeper as a UniqueVinyl", vinylID)
	}
	k.queries.RecordVinylCollection(k.ctx, vinyl.RecordVinylCollectionParams{
		UserID:  k.userID,
		VinylID: vinylID,
	})
	return nil
}

// KeepRecord makes an entry given the VinylID to the user's collection
func (k keeper) PlayRecord(vinylID int64) error {
	if k.checkIfExists(vinylID) {
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

func (k keeper) NumPlays(vinylID int64) int {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.numPlays[vinylID]
}

func (k *keeper) initKeeper(ctx context.Context) error {
	k.mu = &sync.RWMutex{}
	k.ctx = ctx
	// Initialize DB and queries
	if err := k.initializeQueries(ctx); err != nil {
		return err
	}
	vinyls, err := k.queries.GetAllVinyls(k.ctx)
	if err != nil {
		return err
	}
	k.vinylLookup = make(map[int64]vinyl.VinylUnique)
	for _, v := range vinyls {
		k.vinylLookup[v.VinylID] = v
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

func FindClosestVinyl(input Embedding, vinylSlice []Vinyl) Vinyl {
	var closest Vinyl
	maxSimilarity := -1.0
	for _, v := range vinylSlice {
		similarity := cosineSimilarity(input, v.Embedding)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
			closest = v
		}
	}
	return closest
}
