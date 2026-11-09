package projects

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/naay99999/neything/internal/redact"
)

// Patch is an update_project request: only the fields that changed.
type Patch struct {
	Summary, Stack, Status *string
	ReadMore               *[]string // replaces the list
	FactsAdd               []string
	FactsRemove            []int // 1-based, applied before FactsAdd
}

// CapError reports a field over its cap.
type CapError struct {
	Field      string
	Limit, Got int
}

func (e CapError) Error() string {
	if e.Field == "facts" || e.Field == "read_more" {
		return fmt.Sprintf("%s has %d entries; the limit is %d", e.Field, e.Got, e.Limit)
	}
	return fmt.Sprintf("%s is %d characters; the limit is %d", e.Field, e.Got, e.Limit)
}

// clean is the card chokepoint for agent-supplied text: redact secrets, then
// flatten onto one line and strip leading '#' so a value cannot forge card or
// brief structure.
func clean(s string) string {
	s = strings.Join(strings.Fields(redact.String(s)), " ")
	return strings.TrimSpace(strings.TrimLeft(s, "#"))
}

func capRunes(field, s string, limit int) error {
	if n := utf8.RuneCountInString(s); n > limit {
		return CapError{Field: field, Limit: limit, Got: n}
	}
	return nil
}

// Apply applies p to c. It is all-or-nothing: on any error c is unchanged.
func (c *Card) Apply(p Patch) error {
	next := *c
	if p.Summary != nil {
		next.Summary = clean(*p.Summary)
	}
	if p.Stack != nil {
		next.Stack = clean(*p.Stack)
	}
	if p.Status != nil {
		next.Status = clean(*p.Status)
	}
	if p.ReadMore != nil {
		next.ReadMore = nil
		for _, r := range *p.ReadMore {
			if r = clean(r); r != "" {
				next.ReadMore = append(next.ReadMore, r)
			}
		}
	}
	if len(p.FactsRemove) > 0 {
		drop := map[int]bool{}
		for _, i := range p.FactsRemove {
			if i < 1 || i > len(c.Facts) {
				return fmt.Errorf("facts_remove: no fact %d (the card has %d)", i, len(c.Facts))
			}
			drop[i] = true
		}
		var kept []string
		for i, f := range c.Facts {
			if !drop[i+1] {
				kept = append(kept, f)
			}
		}
		next.Facts = kept
	} else {
		next.Facts = append([]string(nil), c.Facts...)
	}
	for _, f := range p.FactsAdd {
		if f = clean(f); f != "" {
			next.Facts = append(next.Facts, f)
		}
	}
	if err := next.validate(); err != nil {
		return err
	}
	*c = next
	return nil
}

func (c Card) validate() error {
	for _, chk := range []struct {
		field, s string
		limit    int
	}{{"summary", c.Summary, MaxSummary}, {"stack", c.Stack, MaxStack}, {"status", c.Status, MaxStatus}} {
		if err := capRunes(chk.field, chk.s, chk.limit); err != nil {
			return err
		}
	}
	if len(c.ReadMore) > MaxReadMore {
		return CapError{Field: "read_more", Limit: MaxReadMore, Got: len(c.ReadMore)}
	}
	for i, r := range c.ReadMore {
		if err := capRunes(fmt.Sprintf("read_more[%d]", i+1), r, MaxReadMoreLen); err != nil {
			return err
		}
	}
	if len(c.Facts) > MaxFacts {
		return CapError{Field: "facts", Limit: MaxFacts, Got: len(c.Facts)}
	}
	for i, f := range c.Facts {
		if err := capRunes(fmt.Sprintf("facts[%d]", i+1), f, MaxFactLen); err != nil {
			return err
		}
	}
	return nil
}
