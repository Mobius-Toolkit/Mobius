// Command mobius runs the Mobius server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	gork "github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/api"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/web"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6363", "address to listen on")
	dbPath := flag.String("db", "mobius.db", "path of the SQLite database")
	flag.Parse()

	if os.Getenv("GORK_EXPORT") == "1" {
		router := api.Routes(http.NewServeMux(), nil)
		spec := gork.GenerateOpenAPI(router.GetRegistry(), gork.WithTitle("Mobius"), gork.WithVersion("0.1.0"))
		if err := json.NewEncoder(os.Stdout).Encode(spec); err != nil {
			log.Fatal(err)
		}
		return
	}

	db, err := store.Open(context.Background(), *dbPath)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	api.Routes(mux, store.New(db))

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/api/", http.NotFoundHandler())
	mux.Handle("/", web.Handler(dist))

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listen on http://%s", *addr)
	log.Fatal(srv.ListenAndServe())
}
