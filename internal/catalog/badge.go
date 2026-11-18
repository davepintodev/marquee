package catalog

import "fmt"

// Badge is a title's or season's state for a tile. Until downloads exist,
// it follows from the catalog alone: a monitored title with no file on
// disk is "searching" (it is wanted and the scheduler will look for it);
// one nobody monitors is "missing".
type Badge struct {
	State        string
	Label        string
	Season       *int
	MissingCount *int
}

var badgeLabels = map[string]string{"downloaded": "Downloaded", "searching": "Searching", "missing": "Missing"}

func simpleBadge(state string) *Badge { return &Badge{State: state, Label: badgeLabels[state]} }

func movieBadge(monitored, haveFile bool) *Badge {
	switch {
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
}

// groupBadge summarises the monitored, aired episodes. It is nil when
// there are none, so callers fall back to what is on disk.
func groupBadge(eps []epView) *Badge {
	var have, gaps, total int
	gapSeason := 0
	gapsIn := map[int]int{}
	for _, e := range eps {
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
