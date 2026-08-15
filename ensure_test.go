package main

import (
	"strings"
	"testing"
)

func TestEnsureAngieIncludesExistingContexts(t *testing.T) {
	input := `events {}
stream {
    resolver 1.1.1.1;
}
http {
    server { listen 8080; }
}
`
	got := ensureHTTPSettings(enableStreamBlock(input))
	_, end, ok := contextRange(got, "stream")
	if !ok {
		t.Fatal("stream context missing")
	}
	open, _, _ := contextRange(got, "stream")
	if !strings.Contains(got[open:end], "include /etc/angie/stream.d/*.conf;") {
		t.Fatal("stream include not inserted into stream context")
	}
	open, end, ok = contextRange(got, "http")
	if !ok {
		t.Fatal("http context missing")
	}
	body := got[open:end]
	for _, required := range []string{
		"include /etc/angie/http.d/*.conf;",
		"resolver 8.8.8.8 1.1.1.1 ipv6=off;",
		"variables_hash_bucket_size 512;",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("http context missing %q", required)
		}
	}
}

func TestEnsureAngieContextsAreNotDuplicated(t *testing.T) {
	input := `stream { include /etc/angie/stream.d/*.conf; }
http {
    resolver 9.9.9.9;
    variables_hash_bucket_size 128;
    include /etc/angie/http.d/*.conf;
}
`
	got := ensureHTTPSettings(enableStreamBlock(input))
	if strings.Count(got, "include /etc/angie/stream.d/*.conf;") != 1 {
		t.Fatal("stream include duplicated")
	}
	if strings.Count(got, "include /etc/angie/http.d/*.conf;") != 1 {
		t.Fatal("http include duplicated")
	}
	streamOpen, streamEnd, _ := contextRange(got, "stream")
	streamBody := got[streamOpen:streamEnd]
	if strings.Count(streamBody, "resolver ") != 1 || !strings.Contains(streamBody, "resolver 1.1.1.1 8.8.8.8 ipv6=off;") {
		t.Fatal("stream resolver should be added once")
	}
	httpOpen, httpEnd, _ := contextRange(got, "http")
	httpBody := got[httpOpen:httpEnd]
	if strings.Count(httpBody, "resolver ") != 1 || !strings.Contains(httpBody, "resolver 9.9.9.9;") {
		t.Fatal("existing HTTP resolver should be preserved")
	}
}

func TestExistingStreamResolverIsPreserved(t *testing.T) {
	input := `stream {
    resolver 9.9.9.9 ipv6=off;
}
http { include /etc/angie/http.d/*.conf; }
`
	got := ensureHTTPSettings(enableStreamBlock(input))
	open, end, ok := contextRange(got, "stream")
	if !ok {
		t.Fatal("stream context missing")
	}
	body := got[open:end]
	if strings.Count(body, "resolver ") != 1 {
		t.Fatalf("expected one stream resolver, got: %s", body)
	}
	if !strings.Contains(body, "resolver 9.9.9.9 ipv6=off;") {
		t.Fatal("existing stream resolver was not preserved")
	}
	if strings.Contains(renderAngieStream(testConfig(), testRules(t)), "resolver ") {
		t.Fatal("generated stream config must not contain resolver")
	}
}
