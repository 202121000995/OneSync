package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/202121000995/OneSync/backend"
	"github.com/202121000995/OneSync/internal/auth"
	"github.com/202121000995/OneSync/internal/task"
)

func main() {
	defaultDataDir, err := dataDirectory()
	if err != nil {
		log.Fatal(err)
	}
	port := flag.Int("port", 8765, "local management port")
	dataDir := flag.String("data-dir", defaultDataDir, "OneSync data directory")
	flag.Parse()

	manager, err := task.NewManager(
		filepath.Join(*dataDir, "tasks.json"),
		unavailableRunnerFactory{},
	)
	if err != nil {
		log.Fatal(err)
	}
	credentials, err := auth.NewCredentialStore(filepath.Join(*dataDir, "credentials"))
	if err != nil {
		log.Fatal(err)
	}
	server, err := backend.NewServer(manager, auth.NewLinkService(), credentials)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("OneSync management page: http://127.0.0.1:%d", *port)
	if err := server.ListenAndServe(ctx, *port); err != nil {
		log.Fatal(err)
	}
}

func dataDirectory() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "OneSync"), nil
}

type unavailableRunnerFactory struct{}

func (unavailableRunnerFactory) Create(context.Context, task.Task) (task.Runner, error) {
	return nil, errors.New("synchronization runtime is not configured yet")
}
