package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"yaocai-spider-go/internal/api"
	"yaocai-spider-go/internal/config"
)

var (
	version   = "3.0.0"
	buildTime = "unknown"
)

func main() {
	port := flag.Int("port", 14391, "HTTP API server port")
	configPath := flag.String("config", "spider_config.json", "Config file path")
	logFile := flag.String("log", "", "Log file path (default: stderr)")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	// Always redirect log to file (default: spider.log next to exe)
	if *logFile == "" {
		exePath, err := os.Executable()
		if err == nil {
			*logFile = filepath.Join(filepath.Dir(exePath), "spider.log")
		} else {
			*logFile = "spider.log"
		}
	}
	logFileHandle, err := os.OpenFile(*logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Printf("⚠️ Failed to open log file: %v, using stderr", err)
	} else {
		log.SetOutput(logFileHandle)
		log.Printf("📝 Logging to %s", *logFile)
	}

	if *showVersion {
		fmt.Printf("yaocai-spider-go %s (built %s)\n", version, buildTime)
		os.Exit(0)
	}

	// Load config
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Printf("⚠️ Config load error: %v, using defaults", err)
		cfg = config.DefaultConfig()
	}
	if *port != 14391 {
		cfg.Port = *port
	}

	log.Printf("🕷️ Yaocai Spider Go v%s", version)
	log.Printf("📋 Config: port=%d, price_threshold=%.0f", cfg.Port, cfg.PriceAnomalyThreshold)

	// Create and start API server
	server := api.NewServer(cfg.Port)

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("🛑 Received signal %v, shutting down...", sig)
		os.Exit(0)
	}()

	if err := server.Start(); err != nil {
		log.Fatalf("❌ Server failed: %v", err)
	}
}
