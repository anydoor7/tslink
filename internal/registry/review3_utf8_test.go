package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"unicode/utf8"
)

func TestReview3InvalidUTF8StoreBoundaries(t *testing.T) {
	path := peopleFixture(t)
	if _, err := ChangePerson(path, "alice\ufffd@example.com", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range []string{"alice\xff@example.com", "alice\xfe@example.com", "\xc0\xaf", "\xed\xa0\x80"} {
		t.Run(string([]byte(login)), func(t *testing.T) {
			for _, call := range []struct {
				name string
				run  func() error
			}{
				{"normalize", func() error { _, err := NormalizePerson(login); return err }},
				{"resolve", func() error { _, err := ResolvePersonLogin(path, login); return err }},
				{"add", func() error { _, err := ChangePerson(path, login, []string{"photos"}, nil, false, false); return err }},
				{"update", func() error { _, err := ChangePerson(path, login, []string{"photos"}, nil, false, true); return err }},
				{"remove", func() error { _, err := RemovePerson(path, login); return err }},
				{"save-invite", func() error {
					return SavePersonInvite(path, login, PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInvitePending})
				}},
			} {
				var coded CodedError
				if err := call.run(); !errors.As(err, &coded) || coded.Code != "usage_error" {
					t.Errorf("%s must reject invalid UTF-8 as usage_error: %v", call.name, err)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid identity changed disk", err)
			}
		})
	}
}

func TestReview3InvalidUTF8WhoIsAuthoritativeDeny(t *testing.T) {
	login := "alice\xff@example.com"
	svc := Service{Name: "photos", Type: TypeProxy, AllowedUsers: []string{login, "tag:trusted"}}
	// Even an in-memory legacy key cannot grandfather malformed WhoIs bytes.
	for _, reg := range []*Registry{{}, {People: []Person{{Login: login, Grants: []PersonGrant{{App: "photos"}}}}}} {
		allowed, authoritative := PeopleAccessAt(reg, svc, login, nil, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
		if allowed || !authoritative {
			t.Fatal("invalid WhoIs must authoritatively deny", allowed, authoritative)
		}
	}
}

func TestReview3ParentJSONCannotPersistInvalidUTF8(t *testing.T) {
	path := peopleFixture(t)
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.SchemaVersion = PeopleRegistrySchemaVersion
	reg.People = []Person{{Login: "alice\xff@example.com", Grants: []PersonGrant{{App: "photos"}}}}
	// This is the same encoding/json persistence boundary as the parent build.
	encoded, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.Valid(encoded) || bytes.Contains(encoded, []byte{0xff}) {
		t.Fatal("JSON retained invalid bytes")
	}
	var decoded Registry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	want := "alice\ufffd@example.com"
	if decoded.People[0].Login != want {
		t.Fatalf("parent serialization login = %q", decoded.People[0].Login)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		loaded, _, err := Preflight(path)
		if err != nil || loaded.People[0].Login != want {
			t.Fatal("load changed stored identity", loaded, err)
		}
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(encoded, actual) {
			t.Fatal("load rewrote parent registry", err)
		}
	}
}
