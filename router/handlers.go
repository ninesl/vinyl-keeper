package router

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/ninesl/vinyl-keeper/internal/values"
	"github.com/ninesl/vinyl-keeper/router/assets/pages"
	"github.com/ninesl/vinyl-keeper/vinyl"
)

// Embedding is a float64 slice
type Embedding []float64

const ConfidenceThreshold = 0.80 // 80%

type ScanHandlerParams struct {
	GetEmbedding           func([]byte) (Embedding, error)
	FindClosestVinylUnqiue func(Embedding) vinyl.VinylUnique
	GetVinyl               func(vinylID int64) *vinyl.VinylUnique
	PlayRecord             func(vinylID int64) error
}

type ScanResult struct {
	Vinyl      vinyl.VinylUnique `json:"vinyl"`
	Found      bool              `json:"found"`
	Similarity float64           `json:"similarity"`
}

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func ScannerPageHandler(w http.ResponseWriter, r *http.Request) {
	pages.ScannerPage(values.TitleScanner).Render(r.Context(), w)
}

func AlbumsPageHandler(getAllVinyls func() []vinyl.VinylUnique) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vinyls := getAllVinyls()
		pages.AlbumsPage(values.TitleAlbums, vinyls).Render(r.Context(), w)
	}
}

func MyVinylPageHandler(getMyVinyl func() []vinyl.VinylWithPlayData) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vinyls := getMyVinyl()
		pages.MyVinylPage(values.TitleMyVinyl, vinyls).Render(r.Context(), w)
	}
}

// ScanCoverHandler takes an jpeg blob as a POST and returns
// a json encoded ScanResult
func ScanCoverHandler(params ScanHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		//TODO: more idiomatic way to verify blob is an type/image and throw a StatusBadRequest if invalid
		// validate it's actually a JPEG (starts with FF D8)
		if len(body) < 3 || body[0] != 0xFF || body[1] != 0xD8 {
			http.Error(w, "invalid JPEG image", http.StatusBadRequest)
			return
		}
		embedding, err := params.GetEmbedding(body)
		if err != nil {
			http.Error(w, "failed to get embedding: "+err.Error(), http.StatusInternalServerError)
			return
		}
		vinylResult := params.FindClosestVinylUnqiue(embedding)
		vinylEmbedding, err := embeddingFromBlob(vinylResult.CoverEmbedding)
		if err != nil {
			http.Error(w, "failed to decode vinyl embedding: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Calculate similarity
		sim := cosineSimilarity(embedding, vinylEmbedding)

		w.Header().Set("Content-Type", values.ContentTypeJSON)
		json.NewEncoder(w).Encode(ScanResult{
			Vinyl:      vinylResult,
			Found:      vinylResult.VinylID != 0,
			Similarity: sim,
		})
	}
}

func ScanCoverHTMLHandler(params ScanHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if r.URL.Query().Get("confirm") == "1" {
			vinylIDStr := r.URL.Query().Get(values.ParamVinylID)
			if vinylIDStr == "" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`<p class="error">Missing vinyl ID</p>`))
				return
			}
			vinylID, err := strconv.ParseInt(vinylIDStr, 10, 64)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`<p class="error">Invalid vinyl ID</p>`))
				return
			}
			if params.GetVinyl == nil {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`<p class="error">Scanner confirmation is not configured</p>`))
				return
			}
			v := params.GetVinyl(vinylID)
			if v == nil {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`<p class="error">Vinyl not found</p>`))
				return
			}
			similarityPercent := 100.0
			if simStr := r.URL.Query().Get("similarity"); simStr != "" {
				if parsed, parseErr := strconv.ParseFloat(simStr, 64); parseErr == nil {
					similarityPercent = parsed
				}
			}
			renderAcceptedScanResult(w, r, params, *v, similarityPercent)
			return
		}

		// Read image bytes
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// Optional: validate it's actually a JPEG (starts with FF D8)
		if len(body) < 3 || body[0] != 0xFF || body[1] != 0xD8 {
			w.Write([]byte(`<p class="error">Invalid image format</p>`))
			return
		}

		// Get embedding from ONNX service
		embedding, err := params.GetEmbedding(body)
		if err != nil {
			w.Write([]byte(`<p class="error">Failed to process image: ` + err.Error() + `</p>`))
			return
		}

		// Find closest vinyl
		vinylResult := params.FindClosestVinylUnqiue(embedding)

		if vinylResult.VinylID == 0 {
			w.Write([]byte(`<p class="error">No matching vinyl found</p>`))
			return
		}

		// Decode the embedding from blob to calculate similarity
		vinylEmbedding, err := embeddingFromBlob(vinylResult.CoverEmbedding)
		if err != nil {
			w.Write([]byte(`<p class="error">Failed to decode vinyl embedding</p>`))
			return
		}

		// Calculate similarity
		sim := cosineSimilarity(embedding, vinylEmbedding)

		// If confidence is below threshold, show choice card
		if sim < ConfidenceThreshold {
			pages.LowConfidenceChoiceCard(vinylResult, sim*100).Render(r.Context(), w)
			return
		}

		renderAcceptedScanResult(w, r, params, vinylResult, sim*100)
	}
}

func renderAcceptedScanResult(w http.ResponseWriter, r *http.Request, params ScanHandlerParams, vinylResult vinyl.VinylUnique, similarityPercent float64) {
	if params.PlayRecord != nil {
		if err := params.PlayRecord(vinylResult.VinylID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<p class="error">Failed to record play: ` + err.Error() + `</p>`))
			return
		}
	}
	pages.ScanResultCard(vinylResult, similarityPercent).Render(r.Context(), w)
}

func cosineSimilarity(a, b Embedding) float64 {
	if len(a) != len(b) || len(a) == 0 {
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

func embeddingFromBlob(b []byte) (Embedding, error) {
	if len(b)%8 != 0 {
		return nil, fmt.Errorf("embedding blob not aligned to float64: %d bytes", len(b))
	}
	emb := make(Embedding, len(b)/8)
	for i := range emb {
		bits := uint64(b[i*8]) | uint64(b[i*8+1])<<8 | uint64(b[i*8+2])<<16 | uint64(b[i*8+3])<<24 |
			uint64(b[i*8+4])<<32 | uint64(b[i*8+5])<<40 | uint64(b[i*8+6])<<48 | uint64(b[i*8+7])<<56
		emb[i] = float64frombits(bits)
	}
	return emb, nil
}

func float64frombits(b uint64) float64 {
	return math.Float64frombits(b)
}

type RegisterHandlerParams struct {
	RegisterVinyl func(artist, album string) (vinyl.VinylUnique, error)
}

type RegisterResult struct {
	Success bool              `json:"success"`
	Vinyl   vinyl.VinylUnique `json:"vinyl,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func RegisterPageHandler(w http.ResponseWriter, r *http.Request) {
	pages.RegisterPage(values.TitleRegister).Render(r.Context(), w)
}

func RegisterSubmitHandler(params RegisterHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		artist := r.URL.Query().Get(values.QueryArtist)
		album := r.URL.Query().Get(values.QueryAlbum)

		if artist == "" || album == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Missing artist or album name</div>`))
			return
		}

		vinylUnique, err := params.RegisterVinyl(artist, album)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<div class="error">` + err.Error() + `</div>`))
			return
		}

		pages.AlbumCard(vinylUnique).Render(r.Context(), w)
	}
}

type DeleteHandlerParams struct {
	DeleteVinyl func(vinylID int64) error
}

func DeleteVinylHandler(params DeleteHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", values.ContentTypeHTML)

		vinylIDStr := r.PathValue(values.ParamVinylID)
		if vinylIDStr == "" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Missing vinyl ID</div>`))
			return
		}

		vinylID, err := strconv.ParseInt(vinylIDStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Invalid vinyl ID</div>`))
			return
		}

		if err := params.DeleteVinyl(vinylID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<div class="error">Failed to delete: ` + err.Error() + `</div>`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(``)) // Empty response - HTMX will remove element
	}
}

type ServeAlbumImageHandlerParams struct {
	GetVinyl func(vinylID int64) *vinyl.VinylUnique
}

func HandleServeAlbumImage(params ServeAlbumImageHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vinylIDStr := r.PathValue(values.ParamVinylID)
		if vinylIDStr == "" {
			http.NotFound(w, r)
			return
		}

		vinylID, err := strconv.ParseInt(vinylIDStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		vinyl := params.GetVinyl(vinylID)
		if vinyl == nil {
			http.NotFound(w, r)
			return
		}

		// Set proper content type based on extension
		contentType := "image/" + vinyl.ImageExtension
		w.Header().Set("Content-Type", contentType)

		// Set caching headers - covers don't change once stored
		w.Header().Set("Cache-Control", "public, max-age=86400") // 24 hours
		w.Header().Set("ETag", fmt.Sprintf("\"%d\"", vinyl.VinylID))

		w.Write(vinyl.CoverRawBlob)
	}
}
