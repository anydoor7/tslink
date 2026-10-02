package registry

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestPeopleIdentityGrammar(t *testing.T) {
	path := peopleFixture(t)
	if p, err := ChangePerson(path, " KELLY@EXAMPLE.COM ", []string{"photos"}, nil, false, false); err != nil || p.Login != "kelly@example.com" {
		t.Fatal(p, err)
	}
	for _, login := range []string{"", "a\u00a0", "a b", "a\x00", "a\u0085b"} {
		before, _ := os.ReadFile(path)
		if _, err := ChangePerson(path, login, []string{"photos"}, nil, false, false); err == nil {
			t.Errorf("malformed login accepted: %q", login)
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Errorf("invalid identity wrote registry: %q", login)
		}
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	svc := Service{Name: "photos", Type: TypeProxy, PeopleScoped: true}
	for _, login := range []string{"\u212aelly@example.com", "\u0130rene@example.com"} {
		allowed, authoritative := PeopleAccessAt(reg, svc, login, nil, time.Now())
		if allowed || !authoritative {
			t.Errorf("distinct Unicode WhoIs must not acquire the ASCII grant: %q", login)
		}
	}
}
