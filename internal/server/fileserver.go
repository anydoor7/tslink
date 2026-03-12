package server

import "net/http"

func NewFileHandler(dir string) http.Handler {
	return http.FileServer(http.Dir(dir))
}
