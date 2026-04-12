package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"

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

	"github.com/ninesl/vinyl-keeper/app/router"
	"github.com/ninesl/vinyl-keeper/app/router/assets/ui"
	"github.com/ninesl/vinyl-keeper/app/router/values"
	"github.com/ninesl/vinyl-keeper/app/vinyl"
)

//go:embed router/assets
var assetsFS embed.FS

func setEmbeddingRoutes(r *router.Router, keeper *keeper) {
	// Search endpoint (JSON response)
	r.Route(http.MethodPost,
		values.EndpointSearch,
		router.ScanCoverHandler(router.ScanHandlerParams{
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
			PlayRecord: keeper.PlayRecord,
			GetUserID:  router.GetUserID,
		}))

	// Search endpoint (HTML response for scanner) — triggers vinyl-registered on play
	r.Route(http.MethodPost,
		values.EndpointSearch+values.EndpointHTMX,
		router.ScanCoverHTMLHandler(router.ScanHandlerParams{
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
			GetVinyl:   keeper.GetVinyl,
			PlayRecord: keeper.PlayRecord,
			GetUserID:  router.GetUserID,
		}))

}

func routeAssets(r *router.Router) {
	assetsSub, err := fs.Sub(assetsFS, "router/assets")
	if err != nil {
		log.Fatalf("failed to create assets fs: %v", err)
	}
	r.Route(http.MethodGet,
		values.EndpointAssets+"/{path...}",
		http.StripPrefix(values.EndpointAssets,
			http.FileServer(http.FS(assetsSub))).ServeHTTP)

}

func main() {
	k, err := NewKeeper()
	if err != nil {
		log.Fatalf("failed to create keeper: %v", err)
	}

	keeper := k.(*keeper)

	if err := waitForImageServiceHealth(); err != nil {
		log.Fatalf("image service health check failed: %v", err)
	}

	r := router.NewRouter()

	r.Route(http.MethodGet, values.EndpointHealth, router.HealthHandler)
	routeAssets(r)

	// Main page - scanner interface
	r.Route(http.MethodGet,
		"/",
		router.ScannerPageHandler(keeper.GetVinylIndex, router.IsUserSignedIn, router.GetUserName),
	)

	// Modal content routes
	r.Route(http.MethodGet,
		values.EndpointModal+"/all-vinyl",
		router.ModalAllVinylHandler(keeper.GetVinylIndex))

	r.Route(http.MethodGet,
		values.EndpointModal+"/my-collection",
		router.ModalMyCollectionHandler(keeper.GetVinylIndex))

	r.Route(http.MethodGet,
		values.EndpointModal+"/register",
		router.RenderHandler(ui.VinylRegisterForm()),
	)

	// Sign-in modal - shows current sign-in status in the panel
	r.Route(http.MethodGet,
		values.EndpointModal+"/sign-in",
		router.SignInPanelHandler(router.GetUserName),
	)

	// Scanner button (OOB swap on sign-in/sign-out events)
	r.Route(http.MethodGet,
		values.EndpointSearch+values.EndpointButton,
		router.ScanButtonHandler(router.IsUserSignedIn))

	// Sign-in related routes
	r.Route(http.MethodGet,
		values.EndpointSignIn+values.EndpointButton,
		router.SignInButtonHandler(keeper))

	r.Route(http.MethodGet,
		values.EndpointSignIn+values.EndpointUsers,
		router.SignInUsersHandler(keeper))

	r.Route(http.MethodPost,
		values.EndpointSignIn+values.EndpointSubmit,
		router.SignInSubmitHandler(keeper))

	r.Route(http.MethodPost,
		values.EndpointSignIn+values.EndpointNew,
		router.SignInCreateUserHandler(keeper))

	// Sign-out route
	r.Route(http.MethodPost,
		values.EndpointSignIn+values.EndpointSignOut,
		router.SignOutHandler())

	// Album filtering routes
	r.Route(http.MethodGet,
		values.EndpointAlbums+values.EndpointFilter,
		router.AlbumsFilterHandler(keeper.AllVinyl, keeper.GetVinylIndex))

	r.Route(http.MethodGet,
		values.EndpointMyVinyl+values.EndpointFilter,
		router.MyVinylFilterHandler(keeper.MyVinyl, keeper.GetVinylIndex, router.GetUserID))

	setEmbeddingRoutes(r, keeper)

	// Register submit (HTMX endpoint) — triggers vinyl-registered on success
	r.Route(http.MethodGet,
		values.EndpointRegister+values.EndpointSubmit,
		router.RegisterSubmitHandler(router.RegisterHandlerParams{
			RegisterVinyl: func(artist, album string) (vinyl.VinylUnique, error) {
				params, err := RegisterUniqueVinylQueryParams(album, artist)
				if err != nil {
					return vinyl.VinylUnique{}, err
				}
				return keeper.RegisterVinylUnique(params)
			},
		}))

	// Delete vinyl (HTMX endpoint) — triggers vinyl-registered on success
	r.Route(http.MethodDelete,
		values.EndpointDelete+"/"+values.PageParam(values.ParamVinylID),
		router.DeleteVinylHandler(router.DeleteHandlerParams{
			DeleteVinyl: func(vinylID int64) error {
				return keeper.DeleteVinyl(vinylID)
			},
		}))

	// Cover image serving
	r.Route(http.MethodGet,
		values.EndpointCover+"/"+values.PageParam(values.ParamVinylID),
		router.HandleServeAlbumImage(router.ServeAlbumImageHandlerParams{
			GetVinyl: keeper.GetVinyl,
		}))

	// Get the router handler and wrap with auth middleware
	baseHandler, err := r.ServeHTTP()
	if err != nil {
		log.Fatalf("failed to setup router: %v", err)
	}

	// Apply auth middleware to all routes
	handler := router.AuthMiddleware(baseHandler)

	addr := ":8080"
	enableTLS := true
	if raw := os.Getenv("ENABLE_TLS"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			log.Fatalf("invalid ENABLE_TLS value %q: %v", raw, err)
		}
		enableTLS = parsed
	}

	if enableTLS {
		certFile := os.Getenv("TLS_CERT")
		keyFile := os.Getenv("TLS_KEY")

		if certFile == "" || keyFile == "" {
			log.Fatal("TLS is enabled but TLS_CERT and TLS_KEY are not set")
		}

		fmt.Printf("Server listening on https://0.0.0.0%s\n", addr)
		fmt.Printf("Using TLS cert: %s\n", certFile)

		if err := http.ListenAndServeTLS(addr, certFile, keyFile, handler); err != nil {
			log.Fatalf("server error: %v", err)
		}
	} else {
		fmt.Printf("Server listening on http://0.0.0.0%s (tls disabled)\n", addr)

		if err := http.ListenAndServe(addr, handler); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}
}
