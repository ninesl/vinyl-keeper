package values

const (
	EndpointHealth   = "/health"
	EndpointScanner  = "/scanner"
	EndpointAlbums   = "/albums"
	EndpointSearch   = "/search"
	EndpointCover    = "/cover"
	EndpointDelete   = "/delete"
	EndpointAssets   = "/assets"
	EndpointRegister = "/register"

	EndpointSubmit = "/submit"
	EndpointStatic = "/static"
	EndpointHTMX   = "/htmx"
)

// URL parameter names
const (
	ParamVinylID = "vinyl_id"
)

// Query parameter keys
const (
	QueryArtist = "artist"
	QueryAlbum  = "album"
)

// Content types
const (
	ContentTypeJSON = "application/json"
	ContentTypeHTML = "text/html"
)

// Page titles
const (
	TitleScanner  = "Vinyl Scanner"
	TitleAlbums   = "Album Collection"
	TitleRegister = "Register Vinyl"
)

// PageParam wraps a parameter name in braces for path patterns
func PageParam(name string) string {
	return "{" + name + "}"
}
