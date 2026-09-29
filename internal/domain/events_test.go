package domain

import (
	"testing"
	"time"
)

func TestEventOn(t *testing.T) {
	cases := []struct {
		name, date, kind, repeat, day, milestone string
		match                                    bool
	}{
		{"birthday", "1990-05-14", "birthday", "", "2026-05-14", "Turns 36 today", true},
		{"newborn", "2026-05-14", "birthday", "", "2026-05-14", "Born today", true},
		{"anniversary", "2012-05-14", "anniversary", "yearly", "2026-05-14", "14 years today", true},
		{"first anniversary", "2025-05-14", "anniversary", "", "2026-05-14", "1 year today", true},
		{"unknown year", "05-14", "birthday", "", "2026-05-14", "", true},
		{"generic event", "2020-05-14", "", "", "2026-05-14", "", true},
		{"wrong day", "1990-05-14", "birthday", "", "2026-05-15", "", false},
		{"wrong month", "1990-05-14", "birthday", "", "2026-06-14", "", false},
		{"future origin", "2027-05-14", "birthday", "", "2026-05-14", "", false},
		{"once", "2026-05-14", "event", "once", "2026-05-14", "", true},
		{"once does not recur", "2025-05-14", "event", "once", "2026-05-14", "", false},
		{"leap birthday", "2000-02-29", "birthday", "", "2028-02-29", "Turns 28 today", true},
		{"leap month day", "02-29", "event", "", "2028-02-29", "", true},
		{"no February fallback", "02-29", "birthday", "", "2027-02-28", "", false},
		{"no March fallback", "02-29", "birthday", "", "2027-03-01", "", false},
		{"invalid date", "02-30", "event", "", "2026-03-02", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			day, err := time.Parse(time.DateOnly, tc.day)
			if err != nil {
				t.Fatal(err)
			}
			event := Event{Name: " Occasion ", Date: tc.date, Type: tc.kind, Repeat: tc.repeat, Description: " A note "}
			item, ok := event.On(day)
			if ok != tc.match || item.Milestone != tc.milestone {
				t.Fatalf("On(%s) = %+v, %v; want milestone %q, match %v", tc.day, item, ok, tc.milestone, tc.match)
			}
			if ok && (item.Name != "Occasion" || item.Description != "A note" || item.Type == "") {
				t.Fatalf("unexpected notice: %+v", item)
			}
		})
	}
}

func TestEventUsesLocalCalendarDate(t *testing.T) {
	instant := time.Date(2026, 5, 13, 23, 30, 0, 0, time.UTC)
	local := instant.In(time.FixedZone("BST", 3600))
	event := Event{Name: "Birthday", Type: "birthday", Date: "1990-05-14"}
	if _, ok := event.On(instant); ok {
		t.Fatal("matched previous UTC date")
	}
	if item, ok := event.On(local); !ok || item.Milestone != "Turns 36 today" {
		t.Fatalf("did not match local calendar date: %+v, %v", item, ok)
	}
}

func TestEventValidation(t *testing.T) {
	for _, date := range []string{"05-14", "02-29", "1990-05-14", "2000-02-29"} {
		if err := (Event{Name: "Occasion", Date: date}).Validate(); err != nil {
			t.Errorf("valid date %q: %v", date, err)
		}
	}
	for _, date := range []string{"", "5-14", "2026-5-14", "0000-05-14", "02-30", "13-01", "2026-02-29", "2026-05-14T12:00:00Z"} {
		if err := (Event{Name: "Occasion", Date: date}).Validate(); err == nil {
			t.Errorf("accepted invalid date %q", date)
		}
	}
	for _, event := range []Event{
		{Name: " ", Date: "05-14"},
		{Name: "Occasion", Date: "05-14", Type: "birthdy"},
		{Name: "Occasion", Date: "05-14", Repeat: "monthly"},
		{Name: "Occasion", Date: "05-14", Repeat: "once"},
	} {
		if err := event.Validate(); err == nil {
			t.Errorf("accepted invalid event %+v", event)
		}
	}
}
