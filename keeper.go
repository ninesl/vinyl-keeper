package main

import (
	"encoding/binary"
	"math"
	"sync"

	"github.com/ninesl/vinyl-keeper/vinyl"
)

type VinylCode int

// func (vd) Name() string {
// 	// TODO: lookup
// }

type VinylError error

type Keeper interface {
	AllPlays() map[VinylCode]int
	SaveRecord(vc VinylCode) error // makes an entry for the record, returns an error if exists already
	PlayRecord(vc VinylCode)       // ++ to the numPlays of the vinyl code, saves the record if not already logged
	NumPlays(vc VinylCode) int     // Number of plays this vinyl code has had in this Keeper
}

type CLIKeeper struct {
	Records map[VinylCode]int
	mu      sync.RWMutex
}

func (k *CLIKeeper) AllPlays() {

}

type Embedding []float64

type Vinyl struct {
	Code      VinylCode
	Embedding Embedding
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

func FindEmbedding(input Embedding, embeddings []Vinyl) Vinyl {
	var closest Vinyl
	maxSimilarity := -1.0

	for _, v := range embeddings {
		similarity := cosineSimilarity(input, v.Embedding)
		if similarity > maxSimilarity {
			maxSimilarity = similarity
			closest = v
		}
	}
	return closest
}

// vinylFromRow converts a sqlc-generated VinylUnique row into our domain Vinyl type.
// CoverEmbedding is stored as raw little-endian float64 bytes (8 bytes per dimension).
func vinylFromRow(row vinyl.VinylUnique) Vinyl {
	emb := make(Embedding, len(row.CoverEmbedding)/8)
	for i := range emb {
		emb[i] = math.Float64frombits(binary.LittleEndian.Uint64(row.CoverEmbedding[i*8:]))
	}
	return Vinyl{
		Code:      VinylCode(row.VinylID),
		Embedding: emb,
	}
}

// embeddingToBytes serializes an Embedding to raw little-endian float64 bytes for SQLite storage.
func embeddingToBytes(emb Embedding) []byte {
	buf := make([]byte, len(emb)*8)
	for i, v := range emb {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}
