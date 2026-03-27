package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	// Embed Mozilla's root CA certificates directly into the binary.
	//
	// WHY: This app makes HTTPS calls to api.discogs.com (see keeper.go:143-156).
	// TLS certificate validation requires trusted root CA certificates to verify
	// that the remote server's certificate is legitimate.
	//
	// Normally, Go reads these from the OS filesystem (e.g., /etc/ssl/certs/).
	// But minimal containers (Alpine, scratch, distroless) often don't include them.
	//
	// WHAT THIS DOES: The x509roots/fallback package embeds ~200KB of Mozilla's
	// root certificates into the compiled binary. When Go can't find system certs,
	// it automatically falls back to these embedded ones.
	//
	// BENEFIT: The binary becomes completely self-contained for HTTPS - no need
	// to install ca-certificates packages or mount cert volumes in containers.
	//
	// Official documentation: https://pkg.go.dev/golang.org/x/crypto/x509roots/fallback
	// Background reading: https://go.dev/blog/certpool
	_ "golang.org/x/crypto/x509roots/fallback"

	"github.com/ninesl/vinyl-keeper/internal/values"
	"github.com/ninesl/vinyl-keeper/router"
	"github.com/ninesl/vinyl-keeper/vinyl"
)

func main() {
	// Initialize keeper
	k, err := NewKeeper()
	if err != nil {
		log.Fatalf("failed to create keeper: %v", err)
	}

	keeper := k.(*keeper)

	// Setup router
	r := router.NewRouter()

	// Health check
	r.Route(http.MethodGet, values.EndpointHealth, router.HealthHandler)

	// Serve static assets
	fileServer := http.FileServer(http.Dir("router/assets/static"))
	staticPath := values.EndpointAssets + "/" + values.SegmentStatic + "/"
	r.Route(http.MethodGet, staticPath, func(w http.ResponseWriter, req *http.Request) {
		http.StripPrefix(staticPath, fileServer).ServeHTTP(w, req)
	})

	// Scanner page
	r.Route(http.MethodGet, values.EndpointScanner, router.ScannerPageHandler)

	// Albums page
	r.Route(http.MethodGet, values.EndpointAlbums, router.AlbumsPageHandler(keeper.AllVinyl))

	// Register page
	r.Route(http.MethodGet, values.EndpointRoot+values.SegmentRegister, router.RegisterPageHandler)

	// Register submit (HTMX endpoint)
	r.Route(http.MethodGet, values.EndpointRoot+values.SegmentRegister+"/"+values.SegmentSubmit, router.RegisterSubmitHandler(router.RegisterHandlerParams{
		RegisterVinyl: func(artist, album string) (vinyl.VinylUnique, error) {
			params, err := RegisterUniqueVinylQueryParams(album, artist)
			if err != nil {
				return vinyl.VinylUnique{}, err
			}
			return keeper.RegisterVinylUnique(params)
		},
	}))

	// Search endpoint (JSON response)
	r.Route(http.MethodPost, values.EndpointSearch, router.ScanCoverHandler(router.ScanHandlerParams{
		GetEmbedding: func(imgData []byte) (router.Embedding, error) {
			emb, err := RequestEmbedding(imgData)
			if err != nil {
				return nil, err
			}
			// Convert Embedding type to router.Embedding
			result := make(router.Embedding, len(emb))
			copy(result, emb)
			return result, nil
		},
		FindClosestVinylUnqiue: func(emb router.Embedding) vinyl.VinylUnique {
			// Convert router.Embedding to main.Embedding
			mainEmb := make(Embedding, len(emb))
			copy(mainEmb, emb)
			return keeper.FindClosestVinyl(mainEmb)
		},
	}))

	// Search endpoint (HTML response for scanner)
	r.Route(http.MethodPost, values.EndpointSearch+"/"+values.SegmentHTML, router.ScanCoverHTMLHandler(router.ScanHandlerParams{
		GetEmbedding: func(imgData []byte) (router.Embedding, error) {
			emb, err := RequestEmbedding(imgData)
			if err != nil {
				return nil, err
			}
			result := make(router.Embedding, len(emb))
			copy(result, emb)
			return result, nil
		},
		FindClosestVinylUnqiue: func(emb router.Embedding) vinyl.VinylUnique {
			mainEmb := make(Embedding, len(emb))
			copy(mainEmb, emb)
			return keeper.FindClosestVinyl(mainEmb)
		},
	}))

	// Delete vinyl
	r.Route(http.MethodDelete, values.EndpointDelete+"/"+values.PageParam(values.ParamVinylID), router.DeleteVinylHandler(router.DeleteHandlerParams{
		DeleteVinyl: func(vinylID int64) error {
			return keeper.DeleteVinyl(vinylID)
		},
	}))

	// Serve album cover images
	r.Route(http.MethodGet, values.EndpointCover+"/"+values.PageParam(values.ParamVinylID), router.HandleServeAlbumImage(router.ServeAlbumImageHandlerParams{
		GetVinyl: keeper.GetVinyl,
	}))

	// Start server
	handler, err := r.ServeHTTP()
	if err != nil {
		log.Fatalf("failed to setup router: %v", err)
	}

	// TLS is REQUIRED - camera access on mobile browsers requires HTTPS
	certFile := os.Getenv("TLS_CERT")
	keyFile := os.Getenv("TLS_KEY")

	if certFile == "" || keyFile == "" {
		log.Fatal("TLS_CERT and TLS_KEY environment variables are required")
	}

	addr := ":8080"
	fmt.Printf("Server listening on https://0.0.0.0%s\n", addr)
	fmt.Printf("Using TLS cert: %s\n", certFile)

	if err := http.ListenAndServeTLS(addr, certFile, keyFile, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
