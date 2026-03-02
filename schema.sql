PRAGMA foreign_keys = ON;

CREATE TABLE vinyl_unique(
	vinyl_id INTEGER PRIMARY KEY AUTOINCREMENT,
	vinyl_title TEXT NOT NULL,
	vinyl_artist TEXT NOT NULL,
	vinyl_pressing_year INTEGER NOT NULL,
	first_pressing INTEGER NOT NULL, -- 0 false 1 true
	image_extension TEXT NOT NULL,
	cover_raw_blob BLOB NOT NULL,
	cover_embedding BLOB NOT NULL,
	UNIQUE (vinyl_title, vinyl_artist, vinyl_pressing_year)
);

CREATE TABLE users(
	user_id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_name TEXT NOT NULL UNIQUE,
	date_created TEXT NOT NULL -- TODO:Parse("Jan 6, 2006")?
);

CREATE TABLE user_vinyl_plays(
	user_id INTEGER NOT NULL,
	vinyl_id INTEGER NOT NULL,
	plays INTEGER NOT NULL,
	first_played TEXT, -- DATE
	last_played TEXT, -- DATE
	PRIMARY KEY (user_id, vinyl_id),
	FOREIGN KEY (user_id) REFERENCES users(user_id),
	FOREIGN KEY (vinyl_id) REFERENCES vinyl_unique(vinyl_id)
);

