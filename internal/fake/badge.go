package fake

import "fmt"

type badge struct {
	State        string `json:"state"`
	Label        string `json:"label"`
	Percent      *int   `json:"percent,omitempty"`
	Season       *int   `json:"season,omitempty"`
	MissingCount *int   `json:"missingCount,omitempty"`
}

func downloading(p int) *badge {
	return &badge{State: "downloading", Label: fmt.Sprintf("Downloading %d%%", p), Percent: &p}
}
func caching(p int) *badge {
	return &badge{State: "caching", Label: fmt.Sprintf("Caching %d%%", p), Percent: &p}
}
func stalled() *badge { return &badge{State: "stalled", Label: "Stalled"} }

// epState is an episode's stored state, overridden while it downloads.
func (f *Fake) epState(t *entry, s *season, e *episode) (state string, percent int) {
	if d := f.activeDownload(t.id(), epID(s, e)); d != nil {
		if d.State == "caching" {
			return "caching", d.Percent
		}
		if d.Stalled {
			return "stalled", 0
		}
		return "downloading", d.Percent
	}
	return e.State, 0
}

func (f *Fake) badge(t *entry) *badge {
	if !t.InLibrary {
		return nil
	}
	if t.Kind == "movie" {
		if d := f.activeDownload(t.id(), ""); d != nil {
			if d.State == "caching" {
				return caching(d.Percent)
			}
			if d.Stalled {
				return stalled()
			}
			return downloading(d.Percent)
		}
		return simple(t.State)
	}
	if b := f.group(t, t.Seasons); b != nil {
		return b
	}
	return unmonitoredBadge(t)
}

// unmonitoredBadge states what is on disk when no episode is monitored,
// so an in-library title never loses its badge.
func unmonitoredBadge(t *entry) *badge {
	aired := 0
	for _, s := range t.Seasons {
		for _, e := range s.Episodes {
			if e.State == "unaired" {
				continue
			}
			if e.State != "downloaded" {
				return simple("missing")
			}
			aired++
		}
	}
	if aired == 0 {
		return simple("missing")
	}
	return simple("downloaded")
}

func simple(state string) *badge {
	labels := map[string]string{"downloaded": "Downloaded", "searching": "Searching", "missing": "Missing"}
	return &badge{State: state, Label: labels[state]}
}

func (f *Fake) seasonBadge(t *entry, s *season) *badge {
	if !t.InLibrary {
		return nil
	}
	return f.group(t, []*season{s})
}

func (f *Fake) group(t *entry, seasons []*season) *badge {
	var have, searching, missing, total int
	var dl, stall, cache bool
	percent, cachePercent := 0, 0
	gapSeason := 0
	for _, s := range seasons {
		seasonGap := false
		for _, e := range s.Episodes {
			if !e.monitored {
				continue
			}
			st, p := f.epState(t, s, e)
			switch st {
			case "unaired":
				continue
			case "downloaded":
				have++
			case "searching":
				searching++
				seasonGap = true
			case "missing":
				missing++
				seasonGap = true
			case "downloading":
				if !dl {
					percent = p
				}
				dl = true
			case "caching":
				if !cache {
					cachePercent = p
				}
				cache = true
			case "stalled":
				stall = true
			}
			total++
		}
		if seasonGap {
			gapSeason = s.Number
		}
	}
	gaps := searching + missing
	switch {
	case total == 0:
		return nil
	case dl:
		return downloading(percent)
	case cache:
		return caching(cachePercent)
	case stall:
		return stalled()
	case have > 0 && gaps > 0:
		return partialSeason(seasons, gapSeason)
	case searching > 0:
		return simple("searching")
	case missing > 0:
		return simple("missing")
	}
	return simple("downloaded")
}

func partialSeason(seasons []*season, number int) *badge {
	n := 0
	for _, s := range seasons {
		if s.Number != number {
			continue
		}
		for _, e := range s.Episodes {
			if e.monitored && (e.State == "missing" || e.State == "searching") {
				n++
			}
		}
	}
	return &badge{State: "partial_season", Label: fmt.Sprintf("Season %d · %d missing", number, n), Season: &number, MissingCount: &n}
}
