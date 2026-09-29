package localapitest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/testenv"
)

func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m, nil))
}

func TestNewClientWithoutStubRefusesEveryRequest(t *testing.T) {
	client := NewClient(nil)
	if !client.OmitAuth || client.Transport == nil {
		t.Fatalf("NewClient(nil) = OmitAuth %v, Transport %v; want OmitAuth and a stub transport", client.OmitAuth, client.Transport)
	}
	if _, err := client.GetPrefs(context.Background()); !errors.Is(err, ErrNoStub) {
		t.Fatalf("GetPrefs() error = %v, want ErrNoStub", err)
	}
}

func TestNewClientSendsRequestsToTheStub(t *testing.T) {
	var paths []string
	client := NewClient(RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.Method+" "+req.URL.Path)
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("request carried Authorization %q; OmitAuth must keep the host token out", got)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"RunSSH":true}`))}, nil
	}))
	prefs, err := client.GetPrefs(context.Background())
	if err != nil || prefs == nil || !prefs.RunSSH {
		t.Fatalf("GetPrefs() = %+v, %v; want the stub's RunSSH=true", prefs, err)
	}
	if strings.Join(paths, ",") != "GET /localapi/v0/prefs" {
		t.Fatalf("stub saw %v, want one GET /localapi/v0/prefs", paths)
	}
}
