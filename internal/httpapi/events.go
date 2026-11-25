package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

const (
	defaultEventsEvery = time.Second
	keepAliveEvery     = 15 * time.Second
	// A client that falls this far behind is dropped; its browser reconnects and refetches.
	subscriberBuffer = 64
)

type event struct {
	name string
	data []byte
}

// hub turns the catalog's state into the contract's event stream. Nothing
// in the catalog announces its writes, so it compares what the catalog says
// from one look to the next; it looks only while a client is listening,
// which keeps an idle marquee idle.
type hub struct {
	a     *api
	every time.Duration

	mu     sync.Mutex
	subs   map[chan event]struct{}
	cancel context.CancelFunc
}

func newHub(a *api, every time.Duration) *hub {
	if every <= 0 {
		every = defaultEventsEvery
	}
	return &hub{a: a, every: every, subs: map[chan event]struct{}{}}
}

// subscribe returns a channel of events and a function that ends the
// subscription. The first subscriber starts the watch, from a baseline
// taken before it returns, so nothing that happens after is missed.
func (h *hub) subscribe(ctx context.Context) (<-chan event, func()) {
	ch := make(chan event, subscriberBuffer)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) == 0 {
		runCtx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go h.watch(runCtx, h.look(ctx))
	}
	h.subs[ch] = struct{}{}
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.subs[ch]; !ok {
			return
		}
		delete(h.subs, ch)
		if len(h.subs) == 0 {
			h.cancel()
		}
	}
}

func (h *hub) broadcast(ctx context.Context, evs []event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	for _, ev := range evs {
		for ch := range h.subs {
			select {
			case ch <- ev:
			default:
				close(ch)
				delete(h.subs, ch)
			}
		}
	}
	if len(h.subs) == 0 {
		h.cancel()
	}
}

// view is what one look at the catalog shows.
type view struct {
	// marks identify a badge without its percent, which progress events carry.
	marks     map[string]string
	badges    map[string]*badgeJSON
	downloads map[string]catalog.Download
	ok        bool
}

func (h *hub) look(ctx context.Context) view {
	v := view{marks: map[string]string{}, badges: map[string]*badgeJSON{}, downloads: map[string]catalog.Download{}}
	titles, err := h.a.Catalog.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.a.Log.Warn("the event stream could not read the library", "err", err)
		}
		return v
	}
	ds, err := h.a.Catalog.ActiveDownloads(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.a.Log.Warn("the event stream could not read the downloads", "err", err)
		}
		return v
	}
	for _, t := range titles {
		b := badgeOf(t.Badge)
		v.badges[t.ID] = b
		v.marks[t.ID] = markOf(b)
	}
	for _, d := range ds {
		v.downloads[d.ID] = d
	}
	v.ok = true
	return v
}

func markOf(b *badgeJSON) string {
	if b == nil {
		return ""
	}
	mark := b.State
	if b.Season != nil {
		mark += fmt.Sprint("/", *b.Season)
	}
	if b.MissingCount != nil {
		mark += fmt.Sprint("/", *b.MissingCount)
	}
	return mark
}

func (h *hub) watch(ctx context.Context, prev view) {
	t := time.NewTicker(h.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		next := h.look(ctx)
		if !next.ok {
			continue
		}
		if prev.ok {
			h.broadcast(ctx, diff(prev, next))
		}
		prev = next
	}
}

// diff is the events that take a client from one view to the next: a
// title event for each badge that changed, appeared or went, one for each
// download that finished or failed (its title may look the same, but its
// history and the queue do not), and a progress event for each download
// that moved.
func diff(prev, next view) []event {
	var evs []event
	send := func(name string, v any) {
		b, _ := json.Marshal(v)
		evs = append(evs, event{name, b})
	}
	titled := map[string]bool{}
	title := func(id string) {
		if titled[id] {
			return
		}
		titled[id] = true
		send("title", map[string]any{"titleId": id, "badge": next.badges[id]})
	}
	for id, mark := range next.marks {
		if old, had := prev.marks[id]; !had || old != mark {
			title(id)
		}
	}
	for id := range prev.marks {
		if _, still := next.marks[id]; !still {
			title(id)
		}
	}
	for id, d := range prev.downloads {
		if _, still := next.downloads[id]; !still {
			title(d.TitleID)
		}
	}
	for id, d := range next.downloads {
		old, had := prev.downloads[id]
		if had && old.State == d.State && old.BytesDone == d.BytesDone && old.BytesTotal == d.BytesTotal && old.CachePercent == d.CachePercent {
			continue
		}
		p := map[string]any{
			"downloadId": d.ID, "titleId": d.TitleID, "state": d.State,
			"percent": d.Percent(), "bytesDone": d.BytesDone, "bytesTotal": d.BytesTotal,
		}
		if ep := episodeRef(d); ep != "" {
			p["episode"] = ep
		}
		send("progress", p)
	}
	return evs
}

func (a *api) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	ch, unsubscribe := a.hub.subscribe(r.Context())
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	beat := time.NewTicker(keepAliveEvery)
	defer beat.Stop()
	for {
		select {
		case <-beat.C:
			_, _ = fmt.Fprint(w, ": keep-alive\n\n")
		case <-r.Context().Done():
			return
		case <-a.Stop:
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data)
		}
		fl.Flush()
	}
}
