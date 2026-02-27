package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"kula-szpiegula/internal/collector"
	"kula-szpiegula/internal/config"
	"kula-szpiegula/internal/storage"
	"kula-szpiegula/internal/tui"
	"kula-szpiegula/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	cmd := "serve"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	// Handle hash-password command first (doesn't need config)
	if cmd == "hash-password" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Enter password: ")
		password, _ := reader.ReadString('\n')
		password = strings.TrimSpace(password)
		web.PrintHashedPassword(password)
		return
	}

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	switch cmd {
	case "serve":
		runServe(cfg)
	case "tui":
		runTUI(cfg)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: kula [serve|tui|hash-password]\n", cmd)
		os.Exit(1)
	}
}

func runServe(cfg *config.Config) {
	coll := collector.New()

	store, err := storage.NewStore(cfg.Storage)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	server := web.NewServer(cfg.Web, coll, store)

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Collection loop
	go func() {
		ticker := time.NewTicker(cfg.Collection.Interval)
		defer ticker.Stop()

		// Initial collection
		sample := coll.Collect()
		store.WriteSample(sample)
		server.BroadcastSample(sample)

		for range ticker.C {
			sample := coll.Collect()
			if err := store.WriteSample(sample); err != nil {
				log.Printf("Storage write error: %v", err)
			}
			server.BroadcastSample(sample)
		}
	}()

	// Start web server
	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Web server error: %v", err)
		}
	}()

	log.Printf("Kula-Szpiegula started (collecting every %s)", cfg.Collection.Interval)
	<-sigCh
	log.Println("Shutting down...")
}

func runTUI(cfg *config.Config) {
	coll := collector.New()
	if err := tui.RunHeadless(coll, cfg.TUI.RefreshRate); err != nil {
		log.Fatalf("TUI error: %v", err)
	}
}
