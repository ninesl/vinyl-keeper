package main

import (
	"fmt"
	"log"
	"net/http"

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
	r.Route(http.MethodGet, "/health", router.HealthHandler)

	// Serve shared CSS
	r.Route(http.MethodGet, "/styles.css", router.StylesHandler)

	// Scanner page
	r.Route(http.MethodGet, "/scanner", router.ScannerPageHandler)

	// Albums page
	r.Route(http.MethodGet, "/albums", router.AlbumsPageHandler(keeper.AllVinyl))

	// Register page
	r.Route(http.MethodGet, "/register", router.RegisterPageHandler)

	// Register submit (HTMX endpoint)
	r.Route(http.MethodGet, "/register/submit", router.RegisterSubmitHandler(router.RegisterHandlerParams{
		RegisterVinyl: func(artist, album string) (vinyl.VinylUnique, error) {
			params, err := RegisterUniqueVinylQueryParams(album, artist)
			if err != nil {
				return vinyl.VinylUnique{}, err
			}
			return keeper.RegisterVinylUnique(params)
		},
	}))

	// Search endpoint (JSON response)
	r.Route(http.MethodPost, "/search", router.ScanCoverHandler(router.ScanHandlerParams{
		GetEmbedding: func(imgData []byte) (router.Embedding, error) {
			emb, err := RequestEmbedding(imgData)
			if err != nil {
				return nil, err
			}
			// Convert Embedding type to router.Embedding
			result := make(router.Embedding, len(emb))
			for i, v := range emb {
				result[i] = v
			}
			return result, nil
		},
		FindClosest: func(emb router.Embedding) vinyl.VinylUnique {
			// Convert router.Embedding to main.Embedding
			mainEmb := make(Embedding, len(emb))
			for i, v := range emb {
				mainEmb[i] = v
			}
			return keeper.FindClosestVinyl(mainEmb)
		},
	}))

	// Search endpoint (HTML response for scanner)
	r.Route(http.MethodPost, "/search/html", router.ScanCoverHTMLHandler(router.ScanHandlerParams{
		GetEmbedding: func(imgData []byte) (router.Embedding, error) {
			emb, err := RequestEmbedding(imgData)
			if err != nil {
				return nil, err
			}
			result := make(router.Embedding, len(emb))
			for i, v := range emb {
				result[i] = v
			}
			return result, nil
		},
		FindClosest: func(emb router.Embedding) vinyl.VinylUnique {
			mainEmb := make(Embedding, len(emb))
			for i, v := range emb {
				mainEmb[i] = v
			}
			return keeper.FindClosestVinyl(mainEmb)
		},
	}))

	// Delete vinyl
	r.Route(http.MethodDelete, "/delete/{vinyl_id}", router.DeleteVinylHandler(router.DeleteHandlerParams{
		DeleteVinyl: func(vinylID int64) error {
			return keeper.DeleteVinyl(vinylID)
		},
	}))

	// Start server
	handler, err := r.ServeHTTP()
	if err != nil {
		log.Fatalf("failed to setup router: %v", err)
	}

	addr := ":8080"
	fmt.Printf("Server listening on %s\n", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
