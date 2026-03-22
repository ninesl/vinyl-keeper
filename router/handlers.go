package router

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"strconv"

	"github.com/ninesl/vinyl-keeper/vinyl"
)

//go:embed templates/scanner.html
var scannerHTML string

//go:embed templates/albums.html
var albumsHTML string

//go:embed templates/register.html
var registerHTML string

//go:embed templates/styles.css
var stylesCSS string

// Embedding is a float64 slice
type Embedding []float64

type ScanHandlerParams struct {
	GetEmbedding func([]byte) (Embedding, error)
	FindClosest  func(Embedding) vinyl.VinylUnique
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

func StylesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")
	w.Write([]byte(stylesCSS))
}

func ScannerPageHandler(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.New("scanner").Parse(scannerHTML)
	if err != nil {
		http.Error(w, "failed to parse template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, "failed to render template: "+err.Error(), http.StatusInternalServerError)
	}
}

type AlbumsPageData struct {
	Vinyls []vinyl.VinylUnique
	Images map[int64]string // base64 encoded images
}

func AlbumsPageHandler(getAllVinyls func() []vinyl.VinylUnique) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := template.New("albums").Funcs(template.FuncMap{
			"derefString": func(s *string) string {
				if s == nil {
					return ""
				}
				return *s
			},
		}).Parse(albumsHTML)
		if err != nil {
			http.Error(w, "failed to parse template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		vinyls := getAllVinyls()

		// Convert images to base64 for template
		images := make(map[int64]string)
		for _, v := range vinyls {
			// Convert []byte to base64 string
			images[v.VinylID] = base64Encode(v.CoverRawBlob)
		}

		data := AlbumsPageData{
			Vinyls: vinyls,
			Images: images,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "failed to render template: "+err.Error(), http.StatusInternalServerError)
		}
	}
}

func base64Encode(data []byte) string {
	const base64Table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	if len(data) == 0 {
		return ""
	}

	// Pre-allocate result buffer
	resultLen := ((len(data) + 2) / 3) * 4
	result := make([]byte, resultLen)

	var j int
	for i := 0; i < len(data); i += 3 {
		// Get 3 bytes
		b0, b1, b2 := data[i], byte(0), byte(0)
		if i+1 < len(data) {
			b1 = data[i+1]
		}
		if i+2 < len(data) {
			b2 = data[i+2]
		}

		// Convert to 4 base64 chars
		result[j] = base64Table[b0>>2]
		result[j+1] = base64Table[((b0&0x03)<<4)|(b1>>4)]
		result[j+2] = base64Table[((b1&0x0f)<<2)|(b2>>6)]
		result[j+3] = base64Table[b2&0x3f]

		j += 4
	}

	// Handle padding
	switch len(data) % 3 {
	case 1:
		result[resultLen-2] = '='
		result[resultLen-1] = '='
	case 2:
		result[resultLen-1] = '='
	}

	return string(result)
}

func ScanCoverHandler(params ScanHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
			http.Error(w, "invalid JPEG image", http.StatusBadRequest)
			return
		}

		// Get embedding from ONNX service
		embedding, err := params.GetEmbedding(body)
		if err != nil {
			http.Error(w, "failed to get embedding: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Find closest vinyl
		vinylResult := params.FindClosest(embedding)

		// Decode the embedding from blob to calculate similarity
		vinylEmbedding, err := embeddingFromBlob(vinylResult.CoverEmbedding)
		if err != nil {
			http.Error(w, "failed to decode vinyl embedding: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Calculate similarity
		sim := cosineSimilarity(embedding, vinylEmbedding)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ScanResult{
			Vinyl:      vinylResult,
			Found:      vinylResult.VinylID != 0,
			Similarity: sim,
		})
	}
}

func ScanCoverHTMLHandler(params ScanHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<p class="error">Invalid image format</p>`))
			return
		}

		// Get embedding from ONNX service
		embedding, err := params.GetEmbedding(body)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<p class="error">Failed to process image: ` + err.Error() + `</p>`))
			return
		}

		// Find closest vinyl
		vinylResult := params.FindClosest(embedding)

		if vinylResult.VinylID == 0 {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<p class="error">No matching vinyl found</p>`))
			return
		}

		// Decode the embedding from blob to calculate similarity
		vinylEmbedding, err := embeddingFromBlob(vinylResult.CoverEmbedding)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte(`<p class="error">Failed to decode vinyl embedding</p>`))
			return
		}

		// Calculate similarity
		sim := cosineSimilarity(embedding, vinylEmbedding)

		// Render the album card template
		tmpl, err := template.New("card").Funcs(template.FuncMap{
			"derefString": func(s *string) string {
				if s == nil {
					return ""
				}
				return *s
			},
		}).Parse(`<div class="album-card" id="vinyl-{{.VinylID}}">
  <img 
    class="album-cover" 
    src="data:image/{{.ImageExtension}};base64,{{.Image}}" 
    alt="{{.VinylTitle}} by {{.VinylArtist}}"
  />
  <div class="album-info">
    <div class="album-title">{{.VinylTitle}}</div>
    <div class="album-artist">{{.VinylArtist}}</div>
    <div class="album-year-genre">{{.VinylPressingYear}}{{if .Genres}} - {{derefString .Genres}}{{end}}</div>
    {{if .Styles}}
    <div class="album-styles">{{derefString .Styles}}</div>
    {{end}}
    <div class="match-confidence">Match: {{printf "%.1f" .Similarity}}%</div>
    <button class="delete-btn" 
            hx-delete="/delete/{{.VinylID}}" 
            hx-target="#vinyl-{{.VinylID}}"
            hx-swap="outerHTML"
            hx-confirm="Are you sure you want to delete this vinyl?">
      Delete
    </button>
  </div>
</div>`)
		if err != nil {
			http.Error(w, "failed to parse card template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Create data for template with base64 image
		data := struct {
			vinyl.VinylUnique
			Image      string
			Similarity float64
		}{
			VinylUnique: vinylResult,
			Image:       base64Encode(vinylResult.CoverRawBlob),
			Similarity:  sim * 100,
		}

		w.Header().Set("Content-Type", "text/html")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "failed to render card: "+err.Error(), http.StatusInternalServerError)
		}
	}
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
	tmpl, err := template.New("register").Parse(registerHTML)
	if err != nil {
		http.Error(w, "failed to parse template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, nil); err != nil {
		http.Error(w, "failed to render template: "+err.Error(), http.StatusInternalServerError)
	}
}

func RegisterSubmitHandler(params RegisterHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		artist := r.URL.Query().Get("artist")
		album := r.URL.Query().Get("album")

		if artist == "" || album == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Missing artist or album name</div>`))
			return
		}

		vinylUnique, err := params.RegisterVinyl(artist, album)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<div class="error">` + err.Error() + `</div>`))
			return
		}

		// Render the album card template
		tmpl, err := template.New("card").Funcs(template.FuncMap{
			"derefString": func(s *string) string {
				if s == nil {
					return ""
				}
				return *s
			},
		}).Parse(`<div class="album-card" id="vinyl-{{.VinylID}}">
  <img 
    class="album-cover" 
    src="data:image/{{.ImageExtension}};base64,{{.Image}}" 
    alt="{{.VinylTitle}} by {{.VinylArtist}}"
  />
  <div class="album-info">
    <div class="album-title">{{.VinylTitle}}</div>
    <div class="album-artist">{{.VinylArtist}}</div>
    <div class="album-year-genre">{{.VinylPressingYear}}{{if .Genres}} - {{derefString .Genres}}{{end}}</div>
    {{if .Styles}}
    <div class="album-styles">{{derefString .Styles}}</div>
    {{end}}
    <button class="delete-btn" 
            hx-delete="/delete/{{.VinylID}}" 
            hx-target="#vinyl-{{.VinylID}}"
            hx-swap="outerHTML"
            hx-confirm="Are you sure you want to delete this vinyl?">
      Delete
    </button>
  </div>
</div>`)
		if err != nil {
			http.Error(w, "failed to parse card template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Create data for template with base64 image
		data := struct {
			vinyl.VinylUnique
			Image string
		}{
			VinylUnique: vinylUnique,
			Image:       base64Encode(vinylUnique.CoverRawBlob),
		}

		w.Header().Set("Content-Type", "text/html")
		if err := tmpl.Execute(w, data); err != nil {
			http.Error(w, "failed to render card: "+err.Error(), http.StatusInternalServerError)
		}
	}
}

type DeleteHandlerParams struct {
	DeleteVinyl func(vinylID int64) error
}

func DeleteVinylHandler(params DeleteHandlerParams) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vinylIDStr := r.PathValue("vinyl_id")
		if vinylIDStr == "" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Missing vinyl ID</div>`))
			return
		}

		vinylID, err := strconv.ParseInt(vinylIDStr, 10, 64)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`<div class="error">Invalid vinyl ID</div>`))
			return
		}

		if err := params.DeleteVinyl(vinylID); err != nil {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`<div class="error">Failed to delete: ` + err.Error() + `</div>`))
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(``)) // Empty response - HTMX will remove element
	}
}
