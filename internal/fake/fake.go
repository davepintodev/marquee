package fake

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// Options tune the simulated downloads. Zero values pick the defaults.
type Options struct {
	// Tick is how often running downloads advance.
	Tick time.Duration
	// Step is how many percent a running download advances per tick.
	Step int
	// KeepAlive is how often an idle event stream gets a comment line so
	// a proxy does not cut it.
	KeepAlive time.Duration
	// ArtDir, when set, holds real posters and backdrops named
	// poster-<titleId>.jpg and backdrop-<titleId>.jpg. A title without a
	// file keeps its generated gradient.
	ArtDir string
	// TorBox plays a marquee with a TorBox key: Health lists TorBox, releases
	// that are not cached are candidates after the cached ones and can be
	// grabbed, and such a grab shows as caching before it downloads.
	TorBox bool
	// SearchFor is how long a search started by Search now or a retry shows
	// as searching before it ends having found nothing. Search now on a
	// series searches its episodes one after another, each for SearchFor.
	SearchFor time.Duration
}

type episode struct {
	Number  int    `json:"number"`
	Name    string `json:"name"`
	AirDate string `json:"airDate"`
	State   string `json:"state"`
	// File names the video file a downloaded episode is in; episodes of one
	// season that name the same file share it.
	File        string `json:"file"`
	monitored   bool
	searchAt    time.Time
	searchUntil time.Time
}

type season struct {
	Number   int        `json:"number"`
	Episodes []*episode `json:"episodes"`
}

type entry struct {
	TmdbID    int       `json:"tmdbId"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	Overview  string    `json:"overview"`
	InLibrary bool      `json:"inLibrary"`
	State     string    `json:"state"`
	Seasons   []*season `json:"seasons"`

	NextSearchIn string `json:"nextSearchIn"`
	Unmonitored  bool   `json:"unmonitored"`

	monitored   bool
	profile     string
	nextSearch  time.Time
	searchUntil time.Time
}

func (e *entry) id() string { return fmt.Sprintf("%s-%d", e.Kind, e.TmdbID) }

type download struct {
	ID          string `json:"id"`
	TitleID     string `json:"titleId"`
	Episode     string `json:"episode,omitempty"`
	ReleaseName string `json:"releaseName"`
	BytesTotal  int64  `json:"bytesTotal"`
	Percent     int    `json:"percent"`
	Stalled     bool   `json:"stalled"`
	State       string `json:"state"`
	// Rate is the bytes a second the download moves while it downloads; a
	// download the fixtures leave without one moves at defaultRate.
	Rate         int64 `json:"bytesPerSecond"`
	resume       string
	resumeStalls bool
}

type release struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Quality   string   `json:"quality"`
	Tags      []string `json:"tags"`
	SizeBytes int64    `json:"sizeBytes"`
	Source    string   `json:"source"`
	Cached    bool     `json:"cached"`
	Drop      string   `json:"drop,omitempty"`
	Message   string   `json:"message,omitempty"`
}

type historyEntry struct {
	ID        string `json:"id"`
	At        string `json:"at"`
	Kind      string `json:"kind"`
	TitleID   string `json:"titleId"`
	TitleName string `json:"titleName"`
	Episode   string `json:"episode,omitempty"`
	Message   string `json:"message"`
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable"`
	// seq numbers the entries in the order they happened, so a cursor names
	// "older than this one" and survives new entries at the head.
	seq int
}

type event struct{ name, data string }

// Fake is the fixture-backed API. Close stops its download simulation.
type Fake struct {
	http.Handler
	opts Options

	mu        sync.Mutex
	titles    []*entry
	downloads []*download
	pausedAll bool
	ended     map[string]bool
	history   []*historyEntry
	lastSeq   int
	releases  map[string][]release
	aboutBy   map[string]aboutEntry
	health    json.RawMessage
	nextID    int
	subs      map[chan event]struct{}

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

func load(name string, v any) {
	data, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		panic(fmt.Sprintf("fixtures/%s: %v", name, err))
	}
}

// New loads the fixtures and starts simulating the downloads in them.
func New(opts Options) *Fake {
	if opts.Tick <= 0 {
		opts.Tick = 2 * time.Second
	}
	if opts.KeepAlive <= 0 {
		opts.KeepAlive = 25 * time.Second
	}
	if opts.Step <= 0 {
		opts.Step = 1
	}
	if opts.SearchFor <= 0 {
		opts.SearchFor = 4 * time.Second
	}
	f := &Fake{
		opts: opts, subs: map[chan event]struct{}{}, ended: map[string]bool{},
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	load("catalog.json", &f.titles)
	load("downloads.json", &f.downloads)
	load("history.json", &f.history)
	load("releases.json", &f.releases)
	load("about.json", &f.aboutBy)
	f.health = mustRaw("health.json")
	if opts.TorBox {
		f.health = withTorBoxCheck(f.health)
	}
	for _, t := range f.titles {
		t.monitored = t.InLibrary && !t.Unmonitored
		t.nextSearch = time.Now().Add(parseIn(t.NextSearchIn))
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				e.monitored = true
				if t.InLibrary && e.State == "missing" {
					e.State = "wanted"
				}
			}
		}
	}
	for i, h := range f.history {
		h.seq = len(f.history) - i
		f.lastSeq = max(f.lastSeq, h.seq)
		if t := f.find(h.TitleID); t != nil && h.TitleName == "" {
			h.TitleName = t.Title
		}
	}
	for _, d := range f.downloads {
		if d.State == "" {
			d.State = "downloading"
		}
		if d.Rate == 0 {
			d.Rate = defaultRate
		}
		if d.State == "paused" {
			d.resume = "downloading"
		}
	}
	f.Handler = f.routes()
	go f.simulate()
	return f
}

const defaultNextSearch = 6 * time.Hour

func parseIn(s string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return defaultNextSearch
}

func mustRaw(name string) json.RawMessage {
	data, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		panic(err)
	}
	return data
}

// Close stops the download simulation and ends open event streams.
func (f *Fake) Close() {
	f.stopOnce.Do(func() { close(f.stop) })
	<-f.done
}

func (f *Fake) newID(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s-new-%d", prefix, f.nextID)
}

func (f *Fake) find(id string) *entry {
	for _, t := range f.titles {
		if t.id() == id {
			return t
		}
	}
	return nil
}

func epID(s *season, e *episode) string { return fmt.Sprintf("s%02de%02d", s.Number, e.Number) }

func (f *Fake) findEpisode(t *entry, id string) *episode {
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			if epID(s, e) == id {
				return e
			}
		}
	}
	return nil
}

func (f *Fake) activeDownload(titleID, ep string) *download {
	for _, d := range f.downloads {
		if d.TitleID == titleID && d.Episode == ep {
			return d
		}
	}
	return nil
}

func (f *Fake) addHistory(kind string, t *entry, ep, msg, reason string, retryable bool) {
	f.lastSeq++
	h := &historyEntry{
		seq: f.lastSeq, ID: f.newID("h"), At: time.Now().UTC().Format(time.RFC3339), Kind: kind,
		TitleID: t.id(), TitleName: t.Title, Episode: ep, Message: msg, Reason: reason, Retryable: retryable,
	}
	f.history = append([]*historyEntry{h}, f.history...)
}

func (f *Fake) emit(name string, v any) {
	data, _ := json.Marshal(v)
	ev := event{name, string(data)}
	for ch := range f.subs {
		select {
		case ch <- ev:
		default: // a slow reader misses an update rather than growing memory
		}
	}
}

func (f *Fake) emitTitle(t *entry) {
	f.emit("title", map[string]any{"titleId": t.id(), "badge": f.badge(t)})
}

// badgeSnapshot lets a handler announce exactly the titles whose badge it changed.
func (f *Fake) badgeSnapshot() map[string]string {
	snap := make(map[string]string, len(f.titles))
	for _, t := range f.titles {
		b, _ := json.Marshal(f.badge(t))
		snap[t.id()] = string(b)
	}
	return snap
}

func (f *Fake) emitChangedBadges(before map[string]string) {
	for _, t := range f.titles {
		if b, _ := json.Marshal(f.badge(t)); string(b) != before[t.id()] {
			f.emitTitle(t)
		}
	}
}

const defaultRate = 12 << 20

// maxRunning is how many files of one title transfer at once; the rest of a queued season waits its turn.
const maxRunning = 3

type progressEvent struct {
	DownloadID     string `json:"downloadId"`
	TitleID        string `json:"titleId"`
	Episode        string `json:"episode,omitempty"`
	State          string `json:"state"`
	Percent        int    `json:"percent"`
	BytesDone      int64  `json:"bytesDone"`
	BytesTotal     int64  `json:"bytesTotal"`
	BytesPerSecond int64  `json:"bytesPerSecond"`
}

func (f *Fake) held(d *download) bool {
	return f.pausedAll && (d.State == "queued" || d.State == "caching" || d.State == "downloading")
}

func (f *Fake) shownState(d *download) string {
	if f.pausedAll && (d.State == "caching" || d.State == "downloading") {
		return "paused"
	}
	return d.State
}

func (f *Fake) rate(d *download) int64 {
	if d.State != "downloading" || d.Stalled || f.pausedAll {
		return 0
	}
	return d.Rate
}

func (f *Fake) progress(d *download) progressEvent {
	return progressEvent{d.ID, d.TitleID, d.Episode, f.shownState(d), d.Percent, d.BytesTotal * int64(d.Percent) / 100, d.BytesTotal, f.rate(d)}
}

func (f *Fake) simulate() {
	defer close(f.done)
	tick := time.NewTicker(f.opts.Tick)
	defer tick.Stop()
	for {
		select {
		case <-f.stop:
			return
		case <-tick.C:
			f.advance()
		}
	}
}

func (f *Fake) advance() {
	f.mu.Lock()
	defer f.mu.Unlock()
	var finished []*download
	running := map[string]int{}
	for _, d := range f.downloads {
		if d.State == "downloading" {
			running[d.TitleID]++
		}
	}
	for _, d := range f.downloads {
		if d.Stalled || d.State == "paused" || f.held(d) {
			continue
		}
		switch d.State {
		case "queued":
			if running[d.TitleID] >= maxRunning {
				continue
			}
			running[d.TitleID]++
			d.State = "downloading"
		case "verifying":
			d.State = "importing"
			finished = append(finished, d)
		case "caching":
			d.Percent = min(100, d.Percent+f.opts.Step)
			if d.Percent == 100 {
				d.State, d.Percent = "downloading", 0
			}
		default:
			d.Percent = min(100, d.Percent+f.opts.Step)
			if d.Percent == 100 {
				d.State = "verifying"
			}
		}
		f.emit("progress", f.progress(d))
	}
	f.downloads = slices.DeleteFunc(f.downloads, func(d *download) bool { return slices.Contains(finished, d) })
	for _, d := range finished {
		f.ended[d.ID] = true
		f.finish(d)
	}
	f.endSearches()
}

func (f *Fake) endSearches() {
	now := time.Now()
	for _, t := range f.titles {
		ended := endSearch(&t.searchUntil, now)
		for _, s := range t.Seasons {
			for _, e := range s.Episodes {
				if endSearch(&e.searchUntil, now) {
					e.searchAt, ended = time.Time{}, true
				}
			}
		}
		if ended {
			f.emitTitle(t)
		}
	}
}

func endSearch(until *time.Time, now time.Time) bool {
	if until.IsZero() || now.Before(*until) {
		return false
	}
	*until = time.Time{}
	return true
}

func running(at, until time.Time) bool {
	now := time.Now()
	return !until.IsZero() && !now.Before(at) && now.Before(until)
}

func queued(e *episode) bool { return time.Now().Before(e.searchAt) }

// startSearch searches a movie now, or an episode from at, as the scheduler
// takes one search at a time.
func (f *Fake) startSearch(t *entry, e *episode, at time.Time) {
	until := at.Add(f.opts.SearchFor)
	t.nextSearch = time.Now().Add(defaultNextSearch)
	if e == nil {
		t.searchUntil = until
		if t.State != "downloaded" && t.monitored {
			t.State = "not_found"
		}
		return
	}
	e.searchAt, e.searchUntil = at, until
	if e.monitored && waiting(e.State) {
		e.State = "not_found"
	}
}

func (f *Fake) finish(d *download) {
	t := f.find(d.TitleID)
	if t == nil {
		return
	}
	name := t.Title
	if d.Episode != "" {
		if e := f.findEpisode(t, d.Episode); e != nil {
			e.State = "downloaded"
		}
		name += " " + strings.ToUpper(d.Episode)
	} else {
		t.State = "downloaded"
	}
	f.addHistory("imported", t, d.Episode, "Imported "+name+" ("+d.ReleaseName+").", "", false)
	f.emitTitle(t)
}

// withTorBoxCheck adds the TorBox check to the health fixture, after AIOStreams.
func withTorBoxCheck(health json.RawMessage) json.RawMessage {
	var h map[string]any
	if err := json.Unmarshal(health, &h); err != nil {
		panic(err)
	}
	var checks []any
	for _, c := range h["checks"].([]any) {
		checks = append(checks, c)
		if c.(map[string]any)["id"] == "aiostreams" {
			checks = append(checks, map[string]any{"id": "torbox", "name": "TorBox", "status": "ok", "message": "Key accepted."})
		}
	}
	h["checks"] = checks
	out, err := json.Marshal(h)
	if err != nil {
		panic(err)
	}
	return out
}
