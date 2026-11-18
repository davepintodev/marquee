package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/davepintodev/marquee/internal/catalog"
)

func dl(id, title, state string, done int64) catalog.Download {
	d := catalog.Download{State: state, BytesDone: done, BytesTotal: 100}
	d.ID, d.TitleID = id, title
	return d
}

func names(evs []event) []string {
	var out []string
	for _, e := range evs {
		var d map[string]any
		_ = json.Unmarshal(e.data, &d)
		id, _ := d["titleId"].(string)
		out = append(out, e.name+" "+id)
	}
	return out
}

func viewOf(marks map[string]string, ds ...catalog.Download) view {
	v := view{marks: marks, badges: map[string]*badgeJSON{}, downloads: map[string]catalog.Download{}, ok: true}
	for id, m := range marks {
		v.badges[id] = &badgeJSON{State: m, Label: m}
	}
	for _, d := range ds {
		v.downloads[d.ID] = d
	}
	return v
}

func TestDiffSaysOnlyWhatChanged(t *testing.T) {
	same := viewOf(map[string]string{"movie-1": "searching"}, dl("d1", "movie-1", "downloading", 10))
	if got := diff(same, same); len(got) != 0 {
		t.Errorf("nothing changed, yet %v", names(got))
	}

	for _, tc := range []struct {
		name       string
		prev, next view
		want       []string
	}{
		{"a badge changes",
			viewOf(map[string]string{"movie-1": "searching"}), viewOf(map[string]string{"movie-1": "downloaded"}),
			[]string{"title movie-1"}},
		{"a title appears",
			viewOf(map[string]string{}), viewOf(map[string]string{"movie-1": "searching"}),
			[]string{"title movie-1"}},
		{"a title goes",
			viewOf(map[string]string{"movie-1": "searching"}), viewOf(map[string]string{}),
			[]string{"title movie-1"}},
		{"bytes move",
			viewOf(map[string]string{"movie-1": "downloading"}, dl("d1", "movie-1", "downloading", 10)),
			viewOf(map[string]string{"movie-1": "downloading"}, dl("d1", "movie-1", "downloading", 20)),
			[]string{"progress movie-1"}},
		{"a download is queued",
			viewOf(map[string]string{"movie-1": "searching"}), viewOf(map[string]string{"movie-1": "searching"}, dl("d1", "movie-1", "queued", 0)),
			[]string{"progress movie-1"}},
		{"a download ends and the badge stays",
			viewOf(map[string]string{"series-1": "partial_season/1/2"}, dl("d1", "series-1", "downloading", 90)),
			viewOf(map[string]string{"series-1": "partial_season/1/2"}),
			[]string{"title series-1"}},
		{"a download ends and the badge changes: one title event",
			viewOf(map[string]string{"movie-1": "downloading"}, dl("d1", "movie-1", "importing", 100)),
			viewOf(map[string]string{"movie-1": "downloaded"}),
			[]string{"title movie-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := names(diff(tc.prev, tc.next))
			if len(got) != len(tc.want) || (len(got) > 0 && got[0] != tc.want[0]) {
				t.Errorf("events = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAClientThatFallsBehindIsDroppedRatherThanBuffered(t *testing.T) {
	h := newHub(nil, time.Hour)
	slow := make(chan event, 1)
	quick := make(chan event, 8)
	h.subs[slow], h.subs[quick] = struct{}{}, struct{}{}
	cancelled := false
	h.cancel = func() { cancelled = true }

	h.broadcast(t.Context(), []event{{"title", []byte("{}")}, {"title", []byte("{}")}})

	if _, open := <-slow; !open {
		t.Fatal("the first event was lost")
	}
	if _, open := <-slow; open {
		t.Error("the slow client was kept after its buffer overflowed")
	}
	if len(quick) != 2 {
		t.Errorf("the quick client got %d events, want 2", len(quick))
	}
	if cancelled {
		t.Error("the watch stopped while a client was listening")
	}
	delete(h.subs, quick)
	h.broadcast(t.Context(), []event{{"title", []byte("{}")}})
	if !cancelled {
		t.Error("the watch kept running with nobody listening")
	}
}
