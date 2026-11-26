package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// FileIdentity is what a file in the library is: a movie (Season and
// Number zero) or an episode. Names is nil until a lookup has asked TMDB
// for the original language and the alternative titles.
type FileIdentity struct {
	TitleID      string
	Kind         string
	TMDBID       int
	IMDbID       string
	Title        string
	Year         int
	Season       int
	Number       int
	EpisodeTitle string
	Names        *TitleNames
}

// TitleNames are the facts about a title's names TMDB is asked for once.
type TitleNames struct {
	OriginalLanguage  string
	AlternativeTitles []string
}

// IdentifyFile finds the movie or episode the catalog records at path,
// relative to the root of the movie library (kind "movie") or the series
// library (kind "series"); a file that holds several episodes answers with
// its first. ErrNotFound when no movie or episode records the file.
func (s *Store) IdentifyFile(ctx context.Context, kind, path string) (FileIdentity, error) {
	q := `SELECT id, kind, tmdb_id, imdb_id, title, year, 0, 0, '', original_language, alternative_titles
		FROM titles WHERE kind = 'movie' AND file_path = ?1 AND file_path != '' ORDER BY id LIMIT 1`
	if kind == "series" {
		q = `SELECT t.id, t.kind, t.tmdb_id, t.imdb_id, t.title, t.year, e.season, e.number, e.name, t.original_language, t.alternative_titles
			FROM episodes e JOIN titles t ON t.id = e.title_id
			WHERE e.file_path = ?1 AND e.file_path != '' AND t.kind = 'series' ORDER BY t.id, e.season, e.number LIMIT 1`
	} else if kind != "movie" {
		return FileIdentity{}, ErrInvalid
	}
	var f FileIdentity
	var lang string
	var alts sql.NullString
	err := s.r.QueryRowContext(ctx, q, path).Scan(&f.TitleID, &f.Kind, &f.TMDBID, &f.IMDbID, &f.Title, &f.Year, &f.Season, &f.Number, &f.EpisodeTitle, &lang, &alts)
	if errors.Is(err, sql.ErrNoRows) {
		return FileIdentity{}, ErrNotFound
	}
	if err != nil {
		return FileIdentity{}, err
	}
	if alts.Valid {
		f.Names, err = decodeNames(lang, alts.String)
	}
	return f, err
}

// SetTitleNames keeps what TMDB said about a title's names; ErrNotFound
// when the title is not in the library.
func (s *Store) SetTitleNames(ctx context.Context, titleID string, n TitleNames) error {
	alts := n.AlternativeTitles
	if alts == nil {
		alts = []string{}
	}
	b, err := json.Marshal(alts)
	if err != nil {
		return err
	}
	r, err := s.w.ExecContext(ctx, `UPDATE titles SET original_language = ?, alternative_titles = ? WHERE id = ?`, n.OriginalLanguage, string(b), titleID)
	if err != nil {
		return err
	}
	if c, _ := r.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

// TitleNamesOf is what SetTitleNames kept for a title, nil when nothing
// has been kept yet; ErrNotFound when the title is not in the library.
func (s *Store) TitleNamesOf(ctx context.Context, titleID string) (*TitleNames, error) {
	var lang string
	var alts sql.NullString
	err := s.r.QueryRowContext(ctx, `SELECT original_language, alternative_titles FROM titles WHERE id = ?`, titleID).Scan(&lang, &alts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || !alts.Valid {
		return nil, err
	}
	return decodeNames(lang, alts.String)
}

func decodeNames(lang, alts string) (*TitleNames, error) {
	n := &TitleNames{OriginalLanguage: lang}
	if err := json.Unmarshal([]byte(alts), &n.AlternativeTitles); err != nil {
		return nil, err
	}
	return n, nil
}
