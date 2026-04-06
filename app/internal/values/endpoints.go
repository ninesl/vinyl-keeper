package values

const (
	EndpointHealth   = "/health"
	EndpointScanner  = "/scanner"
	EndpointAlbums   = "/albums"
	EndpointMyVinyl  = "/myvinyl"
	EndpointSearch   = "/search"
	EndpointCover    = "/cover"
	EndpointDelete   = "/delete"
	EndpointAssets   = "/assets"
	EndpointRegister = "/register"

	EndpointSubmit = "/submit"
	EndpointStatic = "/static"
	EndpointHTMX   = "/htmx"
	EndpointFilter = "/filter"
)

// URL parameter names
const (
	ParamVinylID = "vinyl_id"
)

// Query parameter keys
const (
	QueryArtist     = "artist"
	QueryAlbum      = "album"
	QueryGenre      = "genre"
	QueryStyle      = "style"
	QueryConfirm    = "confirm"
	QuerySimilarity = "similarity"
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
	TitleMyVinyl  = "My Vinyl"
	TitleRegister = "Register Vinyl"
)

// DOM element IDs
const (
	IDAlbumZone    = "album-zone"
	IDFilterArtist = "filter-artist"
	IDResult       = "result"
	IDResults      = "results"
)

// PageParam wraps a parameter name in braces for path patterns
func PageParam(name string) string {
	return "{" + name + "}"
}
