package registry

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreserveHostRegistry(t *testing.T) {
	for _, field := range []string{"", `,"preserve_host":false`, `,"preserve_host":true`} {
		t.Run(field, func(t *testing.T) {
			var svc Service
			if err := json.Unmarshal([]byte(`{"name":"app","type":"proxy","target":"http://127.0.0.1:8080"`+field+`}`), &svc); err != nil {
				t.Fatal(err)
			}
			want := strings.Contains(field, "true")
			if svc.PreserveHost != want {
				t.Fatalf("decoded preserve_host=%t want %t", svc.PreserveHost, want)
			}
			path := filepath.Join(t.TempDir(), "registry.json")
			if _, err := Add(path, svc); err != nil {
				t.Fatal(err)
			}
			reg, err := Load(path)
			if err != nil || len(reg.Services) != 1 || reg.Services[0].PreserveHost != want {
				t.Fatalf("persisted=%+v err=%v", reg, err)
			}
			b, err := json.Marshal(reg.Services[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), `"preserve_host":true`) != want {
				t.Fatalf("wire=%s", b)
			}
		})
	}
	var svc Service
	if err := json.Unmarshal([]byte(`{"name":"app","type":"proxy","target":"http://127.0.0.1:8080","preserve_host":"true"}`), &svc); err == nil || !strings.Contains(err.Error(), "bool") {
		t.Fatalf("non-boolean preserve_host: %v", err)
	}
}

func TestPreserveHostRequiresProxy(t *testing.T) {
	for _, svc := range []Service{
		{Name: "web", Type: TypeProxy, Target: "http://127.0.0.1:8080"},
		{Name: "files", Type: TypeFile, Path: t.TempDir()},
		{Name: "tcp", Type: TypeTCP, Target: "127.0.0.1:8080"},
	} {
		t.Run(svc.Type, func(t *testing.T) {
			if err := ValidateService(svc); err != nil {
				t.Fatalf("default compatibility control: %v", err)
			}
			svc.PreserveHost = true
			err := ValidateService(svc)
			if svc.Type == TypeProxy {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "do not support preserve_host") {
				t.Fatalf("wrong rejection: %v", err)
			}
		})
	}
}
