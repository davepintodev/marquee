package catalog

import (
	"fmt"
	"time"
)

// Badge is a title's or season's state for a tile. Without a download in
// flight, a monitored title with no file on disk is "wanted" while it waits
// for its next scheduled search, "searching" while one runs or a Search now
// request can start, and "not_found" once a search found nothing usable; one
// nobody monitors is "missing". A download in flight shows as
// "downloading" with its percent, "caching" with TorBox's percent while it
// fetches an uncached release, or "stalled" when its bytes stand still.
type Badge struct {
	State        string
	Label        string
	Percent      *int
	Season       *int
	MissingCount *int
}

func cachingBadge(percent int) *Badge {
	return &Badge{State: "caching", Label: fmt.Sprintf("Caching %d%%", percent), Percent: &percent}
}

func downloadingBadge(percent int) *Badge {
	return &Badge{State: "downloading", Label: fmt.Sprintf("Downloading %d%%", percent), Percent: &percent}
}

var badgeLabels = map[string]string{
	"downloaded": "Downloaded", "wanted": "Wanted", "searching": "Searching", "not_found": "Not found", "missing": "Missing", "stalled": "Stalled",
}

func simpleBadge(state string) *Badge { return &Badge{State: state, Label: badgeLabels[state]} }

func movieBadge(monitored, haveFile bool, dl *inFlight, p pending, running bool, now time.Time) (*Badge, time.Time) {
	if dl == nil && !haveFile && (monitored || running || p.asked) {
		state, next := waitState(p, running, now)
		return simpleBadge(state), next
	}
	return movieDiskBadge(haveFile, dl), time.Time{}
}

func movieDiskBadge(haveFile bool, dl *inFlight) *Badge {
	switch {
	case dl != nil && dl.caching:
		return cachingBadge(dl.percent)
	case dl != nil && dl.stalled:
		return simpleBadge("stalled")
	case dl != nil:
		return downloadingBadge(dl.percent)
	case haveFile:
		return simpleBadge("downloaded")
	}
	return simpleBadge("missing")
}

type epView struct {
	season    int
	monitored bool
	state     string
	percent   int
}

// inFlight is what a title's badge needs to know of its unfinished download.
type inFlight struct {
	stalled, caching bool
	percent          int
}

func transferring(state string) bool {
	return state == "downloading" || state == "stalled" || state == "caching"
}

// groupBadge summarises the monitored, aired episodes and any episode being
// downloaded or searched, whatever its monitoring. It is nil when there are
// none, so callers fall back to what is on disk. A download in flight
// outranks one TorBox is still caching, which outranks a stalled one, which
// outranks a search running, which outranks a gap. Gaps say "not_found" only
// when every one of them found nothing.
func groupBadge(eps []epView) *Badge {
	var have, gaps, nothing, total int
	var downloading, stalled, caching, searching bool
	percent, cachePercent := 0, 0
	gapSeason := 0
	gapsIn := map[int]int{}
	for _, e := range eps {
		if transferring(e.state) {
			total++
			switch e.state {
			case "stalled":
				stalled = true
			case "caching":
				if !caching {
					cachePercent = e.percent
				}
				caching = true
			default:
				if !downloading {
					percent = e.percent
				}
				downloading = true
			}
			continue
		}
		if e.state == "searching" {
			searching = true
			if !e.monitored {
				total++
				continue
			}
		}
		asked := e.state == "wanted" || e.state == "not_found"
		if (!e.monitored && !asked) || e.state == "unaired" {
			continue
		}
		total++
		if e.state == "downloaded" {
			have++
			continue
		}
		if e.state == "not_found" {
			nothing++
		}
		gaps++
		gapSeason = e.season
		gapsIn[e.season]++
	}
	switch {
	case total == 0:
		return nil
	case downloading:
		return downloadingBadge(percent)
	case caching:
		return cachingBadge(cachePercent)
	case stalled:
		return simpleBadge("stalled")
	case searching:
		return simpleBadge("searching")
	case have > 0 && gaps > 0:
		n := gapsIn[gapSeason]
		return &Badge{State: "partial_season", Label: fmt.Sprintf("Season %d · %d missing", gapSeason, n), Season: &gapSeason, MissingCount: &n}
	case gaps > 0 && nothing == gaps:
		return simpleBadge("not_found")
	case gaps > 0:
		return simpleBadge("wanted")
	}
	return simpleBadge("downloaded")
}

// unmonitoredBadge says what is on disk when nothing is wanted, so a title
// in the library never loses its badge.
func unmonitoredBadge(eps []epView) *Badge {
	aired := 0
	for _, e := range eps {
		switch e.state {
		case "unaired":
		case "downloaded":
			aired++
		default:
			return simpleBadge("missing")
		}
	}
	if aired == 0 {
		return simpleBadge("missing")
	}
	return simpleBadge("downloaded")
}
