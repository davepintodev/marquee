// Package aiostreams is marquee's Source: it searches an AIOStreams
// instance for the releases of one title or episode.
package aiostreams

import "github.com/davepintodev/marquee/internal/config"

// ParsedFile is the part of AIOStreams' file-name parse that marquee uses.
type ParsedFile struct {
	Title        string `json:"title"`
	Resolution   string `json:"resolution"`
	Quality      string `json:"quality"`
	Encode       string `json:"encode"`
	ReleaseGroup string `json:"releaseGroup"`
	Seasons      []int  `json:"seasons"`
	Episodes     []int  `json:"episodes"`

	AudioTags     []string `json:"audioTags"`
	AudioChannels []string `json:"audioChannels"`
	VisualTags    []string `json:"visualTags"`
	SeasonPack    bool     `json:"seasonPack"`
}

// Stream is one result of a search: one playable file. Its URL is a
// playback link that carries credentials, so it is a Secret and prints as
// a placeholder everywhere.
type Stream struct {
	URL        config.Secret `json:"url"`
	Filename   string        `json:"filename"`
	FolderName string        `json:"folderName"`
	Size       int64         `json:"size"`
	Type       string        `json:"type"`
	Service    string        `json:"service"`
	Cached     bool          `json:"cached"`
	Addon      string        `json:"addon"`
	Indexer    string        `json:"indexer"`
	InfoHash   string        `json:"infoHash"`
	Age        *float64      `json:"age"`
	ParsedFile *ParsedFile   `json:"parsedFile"`
}
