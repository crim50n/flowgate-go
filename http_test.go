package main

import (
	"net/http"
)

type staticHTTPHandler map[string][]byte

func httpHandler(files map[string][]byte) http.Handler {
	return staticHTTPHandler(files)
}

func (h staticHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, ok := h[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}
