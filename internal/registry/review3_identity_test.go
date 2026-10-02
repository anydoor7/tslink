package registry

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReview3IdentityPersistence(t *testing.T) {
	for _, login := range []string{"ALICE.", "tag:SERVER", "ÉCLAIR@EXAMPLE.COM", "éclair@example.com", `a"b\\c,</script>@example.com`, strings.Repeat("X", 1024*1024) + "@example.com"} {
		name := login
		if len(name) > 100 {
			name = "one-megabyte"
		}
		t.Run(name, func(t *testing.T) {
			path := peopleFixture(t)
			want, err := NormalizePerson(login)
			if err != nil {
				t.Fatal(err)
			}
			p, err := ChangePerson(path, login, []string{"photos"}, nil, false, false)
			if err != nil {
				t.Fatal(err)
			}
			reg, _, err := Preflight(path)
			if err != nil || reg.People[0].Login != want || p.Login != want {
				t.Fatal("round trip changed identity", err)
			}
			ok, auth := PeopleAccessAt(reg, reg.Services[0], login, nil, time.Now())
			if !ok || !auth {
				t.Fatal("grant mismatch")
			}
			before, _ := os.ReadFile(path)
			_, _, err = Preflight(path)
			after, _ := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("read rewrote bytes", err)
			}
			if _, err = RemovePerson(path, login); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReview3InvalidUTF8CannotChangeIdentity(t *testing.T) {
	path := peopleFixture(t)
	login := "alice" + string([]byte{0xff}) + "@example.com"
	got, err := NormalizePerson(login)
	if err != nil {
		return
	}
	if got != login {
		t.Fatal("normalizer changed bytes")
	}
	_, err = ChangePerson(path, login, []string{"photos"}, nil, false, false)
	if err != nil {
		return
	}
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("requested=%x stored=%x", []byte(login), []byte(reg.People[0].Login))
	if reg.People[0].Login != login {
		t.Error("accepted login changed bytes in JSON persistence")
	}
	allowed, auth := PeopleAccessAt(reg, reg.Services[0], "alice\ufffd@example.com", nil, time.Now())
	if allowed && auth {
		t.Error("distinct replacement-character identity acquired grant")
	}
}
