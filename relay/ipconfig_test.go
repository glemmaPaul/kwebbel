package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverPublicIPRequiresTwoMatchingServices(t *testing.T) {
	service := func(response string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(response))
		}))
	}

	first := service("1.1.1.1\n")
	defer first.Close()
	second := service("8.8.8.8\n")
	defer second.Close()
	third := service("1.1.1.1\n")
	defer third.Close()

	ip, err := discoverPublicIP(context.Background(), http.DefaultClient, []ipService{
		{name: "first", url: first.URL, normalize: normalizePlainIP},
		{name: "second", url: second.URL, normalize: normalizePlainIP},
		{name: "third", url: third.URL, normalize: normalizePlainIP},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := ip.String(); got != "1.1.1.1" {
		t.Fatalf("expected matching address 1.1.1.1, got %s", got)
	}
}
