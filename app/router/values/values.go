package values

const (
	EndpointHealth   = "/health"
	EndpointAlbums   = "/albums"
	EndpointMyVinyl  = "/myvinyl"
	EndpointSearch   = "/search"
	EndpointCover    = "/cover"
	EndpointDelete   = "/delete"
	EndpointAssets   = "/assets"
	EndpointRegister = "/register"
	EndpointModal    = "/modal"
	EndpointSignIn   = "/signin"
	EndpointUsers    = "/users"
	EndpointButton   = "/button"

	EndpointSubmit = "/submit"
	EndpointHTMX   = "/htmx"
	EndpointFilter = "/filter"
	EndpointNew    = "/new"
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
	QueryUserID     = "user_id"
	QueryUserName   = "user_name"
)

// Cookie names
const (
	CookieUserID = "vk_user_id"
)

// Content types
const (
	ContentTypeJSON = "application/json"
	ContentTypeHTML = "text/html"
)

// Page titles
const (
	TitleScanner = "Vinyl Scanner"
)

// DOM element IDs
const (
	IDResult           = "result"
	IDResults          = "results"
	IDModalZoneContent = "modal-zone-content"
	IDSignInButtonZone = "sign-in-button-zone"
	IDSignInUsersList  = "sign-in-users-list"
	IDSignInErrors     = "sign-in-errors"
)

// PageParam wraps a parameter name in braces for path patterns
func PageParam(name string) string {
	return "{" + name + "}"
}
