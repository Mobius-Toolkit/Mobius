// Command mobius runs the Mobius server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	gork "github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/mobius-go/internal/api"
	"github.com/Mobius-Toolkit/mobius-go/internal/auth"
	"github.com/Mobius-Toolkit/mobius-go/internal/config"
	"github.com/Mobius-Toolkit/mobius-go/internal/runner"
	"github.com/Mobius-Toolkit/mobius-go/internal/setup"
	"github.com/Mobius-Toolkit/mobius-go/internal/store"
	"github.com/Mobius-Toolkit/mobius-go/web"
)

const help = `Usage: mobius [COMMAND]

Commands:
  serve   Start the server (the default)
  init    Write the config file

Options:
  -h, --help  Show this help text

Environment:
  MOBIUS_CONFIG  The path of the config file (default ~/.mobius/config.toml)
  IP             The address to listen on: 127.0.0.1 (the default) or 0.0.0.0
  PORT           The port to listen on (default 6363)
`

func main() {
	if os.Getenv("GORK_EXPORT") == "1" {
		router := api.Routes(http.NewServeMux(), nil, nil)
		spec := gork.GenerateOpenAPI(router.GetRegistry(), gork.WithTitle("Mobius"), gork.WithVersion("0.1.0"))
		if err := json.NewEncoder(os.Stdout).Encode(spec); err != nil {
			log.Fatal(err)
		}
		return
	}

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "serve", "init":
	case "-h", "--help":
		fmt.Print(help)
		return
	default:
		fmt.Fprintf(os.Stderr, "mobius: unknown argument `%s`\n\n%s", command, help)
		os.Exit(2)
	}

	configPath := os.Getenv("MOBIUS_CONFIG")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fail(err)
		}
		configPath = filepath.Join(home, ".mobius", "config.toml")
	}
	if command == "init" {
		if err := setup.Run(context.Background(), os.Stdin, os.Stdout, configPath, os.Getenv("PATH")); err != nil {
			fail(err)
		}
		return
	}
	if err := serve(configPath); err != nil {
		fail(err)
	}
}

func serve(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	programs := []string{"gh", "curl", "tar"}
	for _, b := range cfg.Roles.Bindings() {
		programs = append(programs, runner.Program(b.Binding.Harness))
	}
	slices.Sort(programs)
	missing := runner.Missing(os.Getenv("PATH"), slices.Compact(programs)...)
	for _, program := range missing {
		fmt.Fprintf(os.Stderr, "mobius: `%s` is not on PATH\n", program)
	}
	if len(missing) > 0 {
		os.Exit(1)
	}

	ip := "127.0.0.1"
	switch text := os.Getenv("IP"); text {
	case "", "127.0.0.1":
	case "0.0.0.0":
		ip = "0.0.0.0"
	default:
		return fmt.Errorf("IP: must be 127.0.0.1 or 0.0.0.0, not %s", text)
	}
	port := uint64(6363)
	if text := os.Getenv("PORT"); text != "" {
		if port, err = strconv.ParseUint(text, 10, 16); err != nil {
			return fmt.Errorf("PORT: must be a port number, not %q", text)
		}
	}

	lock, err := store.Lock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	ctx := context.Background()
	dbPath := filepath.Join(cfg.DataDir, "mobius.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("%s: %w", dbPath, err)
	}
	queries := store.New(db)
	a, err := auth.Start(ctx, queries, cfg.AccessPassword)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	api.Routes(mux, queries, a)
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	mux.Handle("/", web.Handler(dist))

	addr := net.JoinHostPort(ip, strconv.FormatUint(port, 10))
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listen on http://%s", addr)
	return srv.ListenAndServe()
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "mobius: %v\n", err)
	os.Exit(1)
}
