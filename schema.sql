PRAGMA foreign_keys = ON;
/*
func searchForImage(albumTitle, artist string) (discogsResp, error) {
	albumTitle = strings.TrimSpace(albumTitle)
	artist = strings.TrimSpace(artist)
	if albumTitle == "" || artist == "" {
		return discogsResp{}, fmt.Errorf("must provide strings for both albumTitle and artist")
	}
	// fmt resp for all three endpoints
	//https://api.discogs.com/database/search?release_title=a%20collection&artist=third%20eye%20blind&format=vinyl&per_page=1&page=1
	//"results" "master_id"
	//https://api.discogs.com/masters/594810
	//"uri" - discogs direct url
	//"genres" - array of styles by string
	//"styles" - array of styles by string
	//"images" [] - the entry with "type" : "primary", "uri" : "https://i.discogs.com/oauwCBqdKyEn_31qZQon5zHrKLSs4k8BDmUvEptK2SI/rs:fit/g:sm/q:90/h:600/w:600/czM6Ly9kaXNjb2dz/LWRhdGFiYXNlLWlt/YWdlcy9SLTMxMTcw/ODYtMTU0MjY3Njk1/OC03NDk2LmpwZWc.jpeg"
	return discogsResp{}, nil
}
*/

CREATE TABLE IF NOT EXISTS vinyl_unique(
	vinyl_id INTEGER PRIMARY KEY AUTOINCREMENT,
	vinyl_title TEXT NOT NULL,
	vinyl_artist TEXT NOT NULL,
	vinyl_pressing_year INTEGER NOT NULL,
	first_pressing INTEGER NOT NULL, -- 0 false 1 true
	discogs_master_id INTEGER,
	styles TEXT, -- comma separated list of styles "Alternative Rock,Punk" etc
	genres TEXT, -- comma separated list of genres "Rock,Heavy Metal" etc
	image_extension TEXT NOT NULL,
	cover_raw_blob BLOB NOT NULL,
	cover_embedding BLOB NOT NULL,
	UNIQUE (vinyl_title, vinyl_artist, vinyl_pressing_year)
);

CREATE TABLE IF NOT EXISTS users(
	user_id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_name TEXT NOT NULL UNIQUE,
	date_created TEXT NOT NULL DEFAULT (date('now'))
);

CREATE TABLE IF NOT EXISTS user_vinyl_plays(
	user_id INTEGER NOT NULL,
	vinyl_id INTEGER NOT NULL,
	plays INTEGER NOT NULL,
	first_played TEXT, -- DATE
	last_played TEXT, -- DATE
	PRIMARY KEY (user_id, vinyl_id),
	FOREIGN KEY (user_id) REFERENCES users(user_id),
	FOREIGN KEY (vinyl_id) REFERENCES vinyl_unique(vinyl_id)
);

