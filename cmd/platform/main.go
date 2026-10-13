package main

import (
	"log"

	"example.com/k8s-platform/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("platform configuration loaded; listen address=%s", cfg.ListenAddr)
}
