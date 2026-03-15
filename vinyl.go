package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"

	"github.com/ninesl/vinyl-keeper/router"
	"github.com/ninesl/vinyl-keeper/vinyl"
)

// Embedding is a dense float64 vector (e.g. 512-d from the ONNX model).
type Embedding []float64

// Vinyl is the domain type used for in-memory search.
type Vinyl struct {
	VinylID int64 // Associated with vinyl.VinylUnique
	// Title     string
	// Artist    string
	Embedding Embedding
}

// func (v Vinyl) VinylUnique(queries vinyl.Queries) vinyl.VinylUnique {
// }

// EmbeddingFromBlob decodes a little-endian float64 BLOB (as stored in SQLite)
// into an Embedding slice.
func EmbeddingFromBlob(b []byte) (Embedding, error) {
	if len(b)%8 != 0 {
		return nil, fmt.Errorf("embedding blob not aligned to float64: %d bytes", len(b))
	}
	emb := make(Embedding, len(b)/8)
	for i := range emb {
		emb[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return emb, nil
}

// EmbeddingToBlob serializes an Embedding to raw little-endian float64 bytes
// for SQLite BLOB storage.
func EmbeddingToBlob(emb Embedding) []byte {
	buf := make([]byte, len(emb)*8)
	for i, v := range emb {
		binary.LittleEndian.PutUint64(buf[i*8:], math.Float64bits(v))
	}
	return buf
}

// AllVinyls loads every row from vinyl_unique via sqlc, converts the raw
// CoverEmbedding BLOBs into []float64 Embedding slices, and returns domain
// Vinyl values ready for cosine search.
func AllVinyls(ctx context.Context, q *vinyl.Queries) ([]Vinyl, error) {
	rows, err := q.GetAllVinyls(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all vinyls: %w", err)
	}

	vinyls := make([]Vinyl, 0, len(rows))
	for _, row := range rows {
		emb, err := EmbeddingFromBlob(row.CoverEmbedding)
		if err != nil {
			return nil, fmt.Errorf("vinyl %d: %w", row.VinylID, err)
		}
		vinyls = append(vinyls, Vinyl{
			Code:      VinylCode(row.VinylID),
			Title:     row.VinylTitle,
			Artist:    row.VinylArtist,
			Embedding: emb,
		})
	}
	return vinyls, nil
}

// RequestEmbedding sends image bytes to the Python ONNX service and returns
// the parsed float64 embedding vector.
func RequestEmbedding(imgData []byte) (Embedding, error) {
	params, err := loadImageEmbedParams(imgData)
	if err != nil {
		return nil, fmt.Errorf("env config: %w", err)
	}

	resp, err := router.SendImageBytes(params)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server error %d: %s", resp.StatusCode, string(body))
	}

	// Binary format: 4 bytes uint32 LE (image byte_length) + N*8 bytes float64 LE (embedding)
	if len(body) < 4 {
		return nil, fmt.Errorf("response too short: %d bytes", len(body))
	}

	emb, err := EmbeddingFromBlob(body[4:])
	if err != nil {
		return nil, fmt.Errorf("parse embedding: %w", err)
	}
	return emb, nil
}

func loadImageEmbedParams(imgData []byte) (router.ImageEmbedParams, error) {
	host := os.Getenv("IMAGE_SERVICE_HOST")
	if host == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_HOST is not set")
	}

	portRaw := os.Getenv("IMAGE_SERVICE_PORT")
	if portRaw == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_PORT is not set")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return router.ImageEmbedParams{}, fmt.Errorf("invalid IMAGE_SERVICE_PORT: %w", err)
	}

	endpoint := os.Getenv("IMAGE_SERVICE_ENDPOINT")
	if endpoint == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_ENDPOINT is not set")
	}

	return router.ImageEmbedParams{
		ImgData:  imgData,
		Host:     host,
		Port:     port,
		Endpoint: endpoint,
	}, nil
}
