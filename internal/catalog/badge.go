package catalog

import "fmt"

// Badge is a title's or season's state for a tile. Without a download in
// flight it follows from the catalog alone: a monitored title with no file
// on disk is "searching" (it is wanted and the scheduler will look for it);
// one nobody monitors is "missing". A download in flight shows as
// "downloading" with its percent, or "stalled" when its bytes stand still.
type Badge struct {
	State        string
	Label        string
	Percent      *int
	Season       *int
	MissingCount *int
}

func downloadingBadge(percent int) *Badge {
	return &Badge{State: "downloading", Label: fmt.Sprintf("Downloading %d%%", percent), Percent: &percent}
}

var badgeLabels = map[string]string{"downloaded": "Downloaded", "searching": "Searching", "missing": "Missing", "stalled": "Stalled"}

func simpleBadge(state string) *Badge { return &Badge{State: state, Label: badgeLabels[state]} }

func movieBadge(monitored, haveFile bool, dl *inFlight) *Badge {
	switch {
	case dl != nil && dl.stalled:
		return simpleBadge("stalled")
	case dl != nil:
		return downloadingBadge(dl.percent)
	case haveFile:
		return simpleBadge("downloaded")
	case monitored:
		return simpleBadge("searching")
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
	stalled bool
	percent int
}

func transferring(state string) bool { return state == "downloading" || state == "stalled" }

// groupBadge summarises the monitored, aired episodes and any episode being
// downloaded, whatever its monitoring. It is nil when there are none, so
// callers fall back to what is on disk. A download in flight outranks a
// stalled one, which outranks a gap.
func groupBadge(eps []epView) *Badge {
	var have, gaps, total int
	var downloading, stalled bool
	percent := 0
	gapSeason := 0
	gapsIn := map[int]int{}
	for _, e := range eps {
		if transferring(e.state) {
			total++
			if e.state == "stalled" {
				stalled = true
			} else {
				if !downloading {
					percent = e.percent
				}
				downloading = true
			}
			continue
		}
		if !e.monitored || e.state == "unaired" {
			continue
		}
		total++
		if e.state == "downloaded" {
			have++
			continue
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
	case stalled:
		return simpleBadge("stalled")
	case have > 0 && gaps > 0:
		n := gapsIn[gapSeason]
		return &Badge{State: "partial_season", Label: fmt.Sprintf("Season %d · %d missing", gapSeason, n), Season: &gapSeason, MissingCount: &n}
	case gaps > 0:
		return simpleBadge("searching")
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
