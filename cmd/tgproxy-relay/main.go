package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func main() {
	var configPath string
	var listen string
	var token string
	var printTokenHash bool
	var printVersion bool
	var checkConfig bool
	flag.StringVar(&configPath, "config", env("TGPROXY_RELAY_CONFIG", ""), "path to config.json")
	flag.StringVar(&listen, "listen", env("TGPROXY_RELAY_LISTEN", ""), "listen address override")
	flag.StringVar(&token, "token", env("TGPROXY_RELAY_TOKEN", ""), "raw token used only to bootstrap local config")
	flag.BoolVar(&printTokenHash, "print-token-hash", false, "print sha256 token hash and exit")
	flag.BoolVar(&printVersion, "version", false, "print relay version and exit")
	flag.BoolVar(&checkConfig, "check-config", false, "validate config and exit")
	flag.Parse()

	if printVersion {
		fmt.Println(relay.Version)
		return
	}

	if printTokenHash {
		if token == "" {
			log.Fatal("-token or TGPROXY_RELAY_TOKEN is required")
		}
		fmt.Println(config.TokenHash(token))
		return
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatal(err)
	}
	if listen != "" {
		cfg.Listen = listen
	}
	if token != "" {
		cfg.Tokens = append(cfg.Tokens, config.Token{
			Name: "env",
			Hash: config.TokenHash(token),
		})
	}

	server, err := relay.NewServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
	if checkConfig {
		fmt.Println("config ok")
		return
	}
	log.Printf("%s %s listening on %s", relay.Name, relay.Version, cfg.Listen)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func loadConfig(path string) (config.Config, error) {
	if path == "" {
		return config.Default(), nil
	}
	return config.LoadFile(path)
}

func env(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
