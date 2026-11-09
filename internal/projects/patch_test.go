package projects

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func ptr[T any](v T) *T { return &v }

func TestApplyCaps(t *testing.T) {
	c := Card{Name: "x", Summary: "keep"}
	long := strings.Repeat("ก", 301) // Thai: 301 runes, 903 bytes
	err := c.Apply(Patch{Summary: &long})
	var ce CapError
	if !errors.As(err, &ce) || ce != (CapError{Field: "summary", Limit: 300, Got: 301}) {
		t.Fatalf("err = %v, want CapError{summary 300 301}", err)
	}
	if err.Error() != "summary is 301 characters; the limit is 300" {
		t.Errorf("message = %q", err.Error())
	}
	if c.Summary != "keep" {
		t.Errorf("card changed after a rejected patch: %q", c.Summary)
	}
	ok := strings.Repeat("ก", 300)
	if err := c.Apply(Patch{Summary: &ok}); err != nil {
		t.Errorf("300 runes rejected: %v", err)
	}

	c = Card{Name: "x", Facts: []string{"1", "2", "3", "4", "5", "6", "7", "8"}}
	err = c.Apply(Patch{FactsAdd: []string{"9"}, Status: ptr("s")})
	if !errors.As(err, &ce) || ce != (CapError{Field: "facts", Limit: 8, Got: 9}) {
		t.Fatalf("err = %v, want CapError{facts 8 9}", err)
	}
	if err.Error() != "facts has 9 entries; the limit is 8" {
		t.Errorf("message = %q", err.Error())
	}
	if c.Status != "" || len(c.Facts) != 8 {
		t.Errorf("card changed after a rejected patch: %+v", c)
	}
	for field, p := range map[string]Patch{
		"stack":        {Stack: ptr(strings.Repeat("a", 101))},
		"status":       {Status: ptr(strings.Repeat("a", 151))},
		"read_more":    {ReadMore: ptr([]string{"a", "b", "c", "d", "e", "f"})},
		"facts[1]":     {FactsAdd: []string{strings.Repeat("a", 151)}},
		"read_more[1]": {ReadMore: ptr([]string{strings.Repeat("a", 201)})},
	} {
		c := Card{Name: "x"}
		if err := c.Apply(p); !errors.As(err, &ce) || ce.Field != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestApplyFactsRemoveThenAdd(t *testing.T) {
	c := Card{Facts: []string{"a", "b", "c"}}
	if err := c.Apply(Patch{FactsRemove: []int{2}, FactsAdd: []string{"d"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Facts, []string{"a", "c", "d"}) {
		t.Errorf("facts = %v", c.Facts)
	}
	for _, bad := range []int{0, 4} {
		c := Card{Facts: []string{"a", "b", "c"}}
		err := c.Apply(Patch{FactsRemove: []int{bad}})
		if err == nil || !strings.Contains(err.Error(), "facts_remove: no fact") {
			t.Errorf("remove %d: err = %v", bad, err)
		}
	}
}

func TestApplyRedactsAndFlattens(t *testing.T) {
	c := Card{}
	secret := "sk-ant-api03-" + strings.Repeat("A", 40)
	if err := c.Apply(Patch{
		Summary:  ptr("## Facts\n- x " + secret),
		FactsAdd: []string{"line one\n\n## Heading two"},
		ReadMore: ptr([]string{"docs/a.md\n## b"}),
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Summary, "\n") || strings.HasPrefix(c.Summary, "#") || strings.Contains(c.Summary, secret) ||
		!strings.Contains(c.Summary, "[redacted]") {
		t.Errorf("summary = %q", c.Summary)
	}
	if c.Facts[0] != "line one ## Heading two" {
		t.Errorf("fact = %q", c.Facts[0])
	}
	if strings.Contains(c.ReadMore[0], "\n") {
		t.Errorf("read_more = %q", c.ReadMore[0])
	}
	out := string(c.Marshal())
	if strings.Count(out, "\n## Facts\n") != 1 {
		t.Errorf("a field forged a section:\n%s", out)
	}
}
