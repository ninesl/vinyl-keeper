package values

// Base path segments
const (
	EndpointRoot    = "/"
	EndpointHealth  = "/health"
	EndpointScanner = "/scanner"
	EndpointAlbums  = "/albums"
	EndpointSearch  = "/search"
	EndpointCover   = "/cover"
	EndpointDelete  = "/delete"
	EndpointAssets  = "/assets"
)

// Nested path segments (no leading slash)
const (
	SegmentRegister = "register"
	SegmentSubmit   = "submit"
	SegmentStatic   = "static"
	SegmentHTML     = "html"
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
