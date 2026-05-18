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

func embeddingRouteConfigs(keeper *keeper) []router.RouteConfig {
	return []router.RouteConfig{
		{
			Endpoint: values.EndpointSearch,
			Method:   http.MethodPost,
			HandlerFunc: router.ScanCoverHandler(router.ScanHandlerParams{
				GetEmbedding: func(imgData []byte) (router.Embedding, error) {
					emb, err := RequestEmbedding(imgData)
					if err != nil {
						return nil, err
					}
					result := make(router.Embedding, len(emb))
					copy(result, emb)
					return result, nil
				},
				FindClosestVinylUnqiue: func(emb router.Embedding) vinyl.VinylRecord {
					mainEmb := make(Embedding, len(emb))
					copy(mainEmb, emb)
					return keeper.FindClosestVinyl(mainEmb)
				},
				PlayRecord: keeper.PlayRecord,
				GetUserID:  router.GetUserID,
			}),
		},
		{
			Endpoint: values.EndpointSearch + values.EndpointHTMX,
			Method:   http.MethodPost,
			HandlerFunc: router.ScanCoverHTMLHandler(router.ScanHandlerParams{
				GetEmbedding: func(imgData []byte) (router.Embedding, error) {
					emb, err := RequestEmbedding(imgData)
					if err != nil {
						return nil, err
					}
					result := make(router.Embedding, len(emb))
					copy(result, emb)
					return result, nil
				},
				FindClosestVinylUnqiue: func(emb router.Embedding) vinyl.VinylRecord {
					mainEmb := make(Embedding, len(emb))
					copy(mainEmb, emb)
					return keeper.FindClosestVinyl(mainEmb)
				},
				FindClosestVinyls: func(emb router.Embedding, n int) []vinyl.VinylRecord {
					mainEmb := make(Embedding, len(emb))
					copy(mainEmb, emb)
					return keeper.FindClosestVinyls(mainEmb, n)
				},
				FindClosestReleaseCandidates: func(emb router.Embedding, n int, userID int64) []vinyl.ReleaseCandidate {
					mainEmb := make(Embedding, len(emb))
					copy(mainEmb, emb)
					return keeper.FindClosestReleaseCandidates(mainEmb, n, userID)
				},
				GetVinyl:            keeper.GetVinyl,
				GetReleaseCandidate: keeper.GetReleaseCandidate,
				PlayRecord:          keeper.PlayRecord,
				PlayRecordRelease:   keeper.PlayRecordRelease,
				GetUserID:           router.GetUserID,
			}),
		},
	}
}

func assetRouteConfig() router.RouteConfig {
	assetsSub, err := fs.Sub(assetsFS, "router/assets")
	if err != nil {
		log.Fatalf("failed to create assets fs: %v", err)
	}
	return router.RouteConfig{
		Endpoint:    values.EndpointAssets + "/{path...}",
		Method:      http.MethodGet,
		HandlerFunc: http.StripPrefix(values.EndpointAssets, http.FileServer(http.FS(assetsSub))).ServeHTTP,
	}
}

func MiddlewareInit(r *router.Router) {
	r.Use(router.RecoveryMiddleware)
	r.Use(router.AuthMiddleware)
	r.Use(router.LoggingMiddleware)
}

func main() {
	log.Println("[Init] Creating keeper")
	k, err := NewKeeper()
	if err != nil {
		log.Fatalf("failed to create keeper: %v", err)
	}

	keeper := k.(*keeper)
	log.Println("[Init] Keeper created successfully")

	log.Println("[Init] Waiting for image service health check")
	if err := waitForImageServiceHealth(); err != nil {
		log.Fatalf("image service health check failed: %v", err)
	}
	log.Println("[Init] Image service health check passed")

	log.Println("[Init] Creating router")
	r := router.NewRouter()

	MiddlewareInit(r)
	log.Println("[Init] Middleware registration complete")

	routes := []router.RouteConfig{
		{Endpoint: values.EndpointHealth,
			Method:      http.MethodGet,
			HandlerFunc: router.HealthHandler},
		assetRouteConfig(),
		{Endpoint: "/",
			Method:      http.MethodGet,
			HandlerFunc: router.ScannerPageHandler()},
		{Endpoint: values.EndpointModal + values.EndpointMyCollection,
			Method:      http.MethodGet,
			HandlerFunc: router.ModalMyCollectionHandler(keeper.GetVinylIndex)},
		{Endpoint: values.EndpointModal + values.EndpointMyCollection + "/" +
			values.PageParam(values.ParamVinylID) + values.EndpointPressings,
			Method: http.MethodGet,
			HandlerFunc: router.PressingChoiceModalHandler(router.PressingModalHandlerParams{
				GetUserID:         router.GetUserID,
				GetVinyl:          keeper.GetVinyl,
				ListPressingItems: keeper.ListPressingOptions,
			})},
		{Endpoint: values.EndpointModal + values.EndpointRegister,
			Method:      http.MethodGet,
			HandlerFunc: router.RenderHandler(ui.VinylRegisterForm())},
		{Endpoint: values.EndpointModal + values.EndpointSignInModal,
			Method:      http.MethodGet,
			HandlerFunc: router.SignInPanelHandler()},

		{Endpoint: values.EndpointSearch + values.EndpointButton,
			Method:      http.MethodGet,
			HandlerFunc: router.ScanButtonHandler(router.IsUserSignedIn)},

		{Endpoint: values.EndpointSignIn + values.EndpointButton,
			Method:      http.MethodGet,
			HandlerFunc: router.SignInButtonHandler(keeper)},
		{Endpoint: values.EndpointSignIn + values.EndpointBootstrap,
			Method:      http.MethodGet,
			HandlerFunc: router.SignInBootstrapHandler()},
		{Endpoint: values.EndpointSignIn + values.EndpointUsers,
			Method:      http.MethodGet,
			HandlerFunc: router.SignInUsersListHandler(keeper)},
		{Endpoint: values.EndpointSignIn + values.EndpointSubmit,
			Method:      http.MethodPost,
			HandlerFunc: router.SignInSubmitHandler(keeper)},
		{Endpoint: values.EndpointSignIn + values.EndpointNew,
			Method:      http.MethodPost,
			HandlerFunc: router.SignInCreateUserHandler(keeper)},
		{Endpoint: values.EndpointSignIn + values.EndpointUserDelete,
			Method:      http.MethodPost,
			HandlerFunc: router.SignInDeleteUserHandler(keeper)},
		{Endpoint: values.EndpointSignIn + values.EndpointSignOut,
			Method:      http.MethodPost,
			HandlerFunc: router.SignOutHandler()},

		{Endpoint: values.EndpointAuthButtons,
			Method:      http.MethodGet,
			HandlerFunc: router.NavAuthButtonsHandler()},

		{Endpoint: values.EndpointMyVinyl + values.EndpointFilter,
			Method: http.MethodGet,
			HandlerFunc: router.MyVinylFilterHandler(router.MyVinylFilterHandlerParams{
				GetMyVinyl: keeper.MyVinyl,
				GetIndex:   keeper.GetVinylIndex,
				GetUserID:  router.GetUserID,
			})},
		{Endpoint: values.EndpointMyVinyl + values.EndpointRelease + values.EndpointChange,
			Method: http.MethodPost,
			HandlerFunc: router.ChangePressingHandler(router.ChangePressingHandlerParams{
				GetUserID:          router.GetUserID,
				ChangeUserPressing: keeper.ChangeUserPressing,
				GetIndex:           keeper.GetVinylIndex,
			})},
		{Endpoint: values.EndpointMyVinyl + values.EndpointDelete + "/" + values.PageParam(values.ParamVinylID),
			Method: http.MethodDelete,
			HandlerFunc: router.DeleteUserVinylHandler(router.DeleteUserVinylHandlerParams{
				GetUserID:       router.GetUserID,
				DeleteUserVinyl: keeper.DeleteUserVinyl,
				GetIndex:        keeper.GetVinylIndex,
			})},

		{Endpoint: values.EndpointRegister + values.EndpointSubmit,
			Method: http.MethodPost,
			HandlerFunc: router.RegisterSubmitHandler(router.RegisterHandlerParams{
				RegisterVinyl:     keeper.RegisterVinylFromSearch(FindDiscogsMasterID),
				RegisterVinylID:   keeper.RegisterVinylFromMaster,
				FindExistingVinyl: keeper.FindExistingVinyl,
				GetUserID:         router.GetUserID,
			})},

		{Endpoint: values.EndpointCover + "/" +
			values.PageParam(values.ParamVinylID),
			Method: http.MethodGet,
			HandlerFunc: router.HandleServeAlbumImage(router.ServeAlbumImageHandlerParams{
				GetVinyl:        keeper.GetVinyl,
				GetReleaseCover: keeper.GetReleaseCover,
			})},

		{Endpoint: values.EndpointCover + "/" +
			values.PageParam(values.ParamVinylID) + "/" + values.PageParam(values.ParamReleaseID),
			Method: http.MethodGet,
			HandlerFunc: router.HandleServeAlbumImage(router.ServeAlbumImageHandlerParams{
				GetVinyl:        keeper.GetVinyl,
				GetReleaseCover: keeper.GetReleaseCover,
			})},
	}
	routes = append(routes, embeddingRouteConfigs(keeper)...)
	if err := r.RegisterRoutes(routes...); err != nil {
		log.Fatalf("failed to register routes: %v", err)
	}
	log.Println("[Init] Route registration complete")

	// FIXME: unused currently
	//
	// Delete vinyl — triggers vinyl-registered on success
	/*
		r.Route(http.MethodDelete,
			values.EndpointDelete+"/"+values.PageParam(values.ParamVinylID),
			router.DeleteVinylHandler(router.DeleteHandlerParams{
				DeleteVinyl: func(vinylID int64) error {
					return keeper.DeleteVinyl(vinylID)
				},
			}))
	*/

	log.Println("[Init] Router setup complete")

	portAddr := ":8080"
	server := router.NewServerWithRouter(portAddr, r)
	enableTLS := true
	log.Println("[Init] Configuring TLS settings")
	if raw := os.Getenv("ENABLE_TLS"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			log.Fatalf("invalid ENABLE_TLS value %q: %v", raw, err)
		}
		enableTLS = parsed
	}

	if enableTLS {
		log.Println("[Init] TLS enabled")
		certFile := os.Getenv("TLS_CERT")
		keyFile := os.Getenv("TLS_KEY")

		if certFile == "" || keyFile == "" {
			log.Fatal("TLS is enabled but TLS_CERT and TLS_KEY are not set")
		}
		fmt.Printf("Server listening on https://0.0.0.0%s\n", portAddr)
		fmt.Printf("Using TLS cert: %s\n", certFile)
		log.Println("[Init] Starting HTTPS server")
		if err := server.ListenAndServeTLS(certFile, keyFile); err != nil {
			log.Fatalf("server error: %v", err)
		}
		log.Println("[Shutdown] Server shutting down gracefully")
	} else {
		log.Println("[Init] TLS disabled")
		fmt.Printf("Server listening on http://0.0.0.0%s (tls disabled)\n", portAddr)
		log.Println("[Init] Starting HTTP server")
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("server error: %v", err)
		}
		log.Println("[Shutdown] Server shutting down gracefully")
	}
}
