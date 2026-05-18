package server

import (
	"net/http"
	"os"
)

func NewFileHandler(dir string) http.Handler {
	return http.FileServer(http.FS(os.DirFS(dir)))
}
