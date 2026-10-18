// Package fakewhisper is the program whisper-server of the tests. It serves GET /health and POST /inference.
//
// The program writes one line with its arguments to the file program+".starts" at each start.
// While the file program+".loading" exists, /health answers 503. /inference answers the number of bytes of the WAV
// file as the text "<n> bytes", and answers 400 for a file that does not start with "RIFF".
package fakewhisper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Run starts the server for the arguments args of whisper-server, and gives the error that ends it. program is the path
// of the program.
func Run(program string, args []string) error {
	var host, port string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--host":
			host = args[i+1]
		case "--port":
			port = args[i+1]
		}
	}
	starts, err := os.OpenFile(filepath.Clean(program+".starts"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(starts, strings.Join(args, " "))
	if closeErr := starts.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := os.Stat(program + ".loading"); err == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /inference", inference)
	server := &http.Server{Addr: net.JoinHostPort(host, port), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return server.ListenAndServe()
}

func inference(w http.ResponseWriter, r *http.Request) {
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()
	wav, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !bytes.HasPrefix(wav, []byte("RIFF")) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "failed to read audio data"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"text": fmt.Sprintf(" %d bytes\n", len(wav))})
}
