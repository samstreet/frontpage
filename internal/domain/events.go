package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is an occasion from the feed configuration. Dates may include an
// original year (for ages and milestones) or just a month and day.
type Event struct {
	Name        string `yaml:"name" json:"name"`
	Date        string `yaml:"date" json:"date"`
	Type        string `yaml:"type" json:"type"`
	Repeat      string `yaml:"repeat" json:"repeat"`
	Description string `yaml:"description" json:"description"`
}

// EditionEvent is a snapshot, including the milestone at publication time.
type EditionEvent struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Milestone   string `json:"milestone,omitempty"`
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.Name) == "" {
		return errors.New("name is required")
	}
	if utf8.RuneCountInString(e.Name) > 200 || utf8.RuneCountInString(e.Description) > 1000 {
		return errors.New("name must be at most 200 characters and description at most 1000 characters")
	}
	if _, _, err := e.parsedDate(); err != nil {
		return err
	}
	switch e.Type {
	case "", "birthday", "anniversary", "event":
	default:
		return errors.New("type must be birthday, anniversary, or event")
	}
	switch e.Repeat {
	case "", "yearly":
	case "once":
		if len(e.Date) != 10 {
			return errors.New("repeat: once requires a YYYY-MM-DD date")
		}
	default:
		return errors.New("repeat must be yearly or once")
	}
	return nil
}

func (e Event) parsedDate() (time.Time, bool, error) {
	raw := e.Date
	hasYear := len(raw) == 10
	if len(raw) == 5 {
		// A leap year allows recurring February 29 occasions.
		raw = "2000-" + raw
	} else if !hasYear {
		return time.Time{}, false, errors.New("date must be YYYY-MM-DD or MM-DD")
	}
	date, err := time.Parse(time.DateOnly, raw)
	if err != nil || date.Year() < 1 || date.Format(time.DateOnly) != raw {
		return time.Time{}, false, errors.New("date must be a valid YYYY-MM-DD or MM-DD date")
	}
	return date, hasYear, nil
}

// On matches the calendar date in day's location. February 29 events appear
// only on February 29; they do not move to another date in non-leap years.
func (e Event) On(day time.Time) (EditionEvent, bool) {
	date, hasYear, err := e.parsedDate()
	if err != nil || date.Month() != day.Month() || date.Day() != day.Day() {
		return EditionEvent{}, false
	}
	if hasYear && day.Year() < date.Year() {
		return EditionEvent{}, false
	}
	if e.Repeat == "once" && (!hasYear || day.Year() != date.Year()) {
		return EditionEvent{}, false
	}
	item := EditionEvent{Name: strings.TrimSpace(e.Name), Type: e.Type, Description: strings.TrimSpace(e.Description)}
	if item.Type == "" {
		item.Type = "event"
	}
	if hasYear {
		years := day.Year() - date.Year()
		switch item.Type {
		case "birthday":
			if years == 0 {
				item.Milestone = "Born today"
			} else {
				item.Milestone = fmt.Sprintf("Turns %d today", years)
			}
		case "anniversary":
			if years == 1 {
				item.Milestone = "1 year today"
			} else if years > 1 {
				item.Milestone = fmt.Sprintf("%d years today", years)
			}
		}
	}
	return item, true
}
