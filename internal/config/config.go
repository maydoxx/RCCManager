package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	GameURL    string
	GameSecret string

	// never reachable from the internet
	RenderURLs   []string
	RenderSecret string

	GameServerScript string

	// lan, not cloudflare
	SiteInternalURL string
	// MachineAddress in Join.ashx, ipv4 only
	SitePublicAddress string

	R2AccountID       string
	R2Endpoint        string
	R2Bucket          string
	R2Prefix          string
	R2AccessKeyID     string
	R2SecretAccessKey string
	CDNURL            string

	PortMin int
	PortMax int

	Listen   string
	Password string

	StateFile string

	IdleTimeout    time.Duration
	Lease          time.Duration
	ReapEvery      time.Duration
	PlayerCountTTL time.Duration
	ReadyTimeout   time.Duration
}

// .env from cwd then next to the binary, real env vars win
func Load() (*Config, error) {
	loadDotEnv(".env")
	if exe, err := os.Executable(); err == nil {
		loadDotEnv(filepath.Join(filepath.Dir(exe), ".env"))
	}

	c := &Config{
		GameURL:           env("RCC_GAME_URL", "http://localhost:64989/"),
		GameSecret:        env("RCC_GAME_SECRET", ""),
		RenderSecret:      env("RCC_RENDER_SECRET", ""),
		GameServerScript:  env("GAMESERVER_SCRIPT", ""),
		SiteInternalURL:   strings.TrimRight(env("SITE_INTERNAL_URL", ""), "/"),
		SitePublicAddress: env("SITE_PUBLIC_ADDRESS", ""),
		R2AccountID:       env("R2_ACCOUNT_ID", ""),
		R2Endpoint:        env("R2_ENDPOINT", ""),
		R2Bucket:          env("R2_BUCKET", ""),
		R2Prefix:          env("R2_PREFIX", ""),
		R2AccessKeyID:     env("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey: env("R2_SECRET_ACCESS_KEY", ""),
		CDNURL:            env("CDN_URL", ""),
		PortMin:           envInt("PORT_MIN", 3023),
		PortMax:           envInt("PORT_MAX", 3072),
		Listen:            env("LISTEN", "127.0.0.1:25500"),
		Password:          env("PASSWORD", ""),
		StateFile:         env("STATE_FILE", "rccmanager-state.json"),
		IdleTimeout:       envDuration("IDLE_TIMEOUT", 10*time.Minute),
		Lease:             envDuration("LEASE", 24*time.Hour),
		ReapEvery:         envDuration("REAP_EVERY", 30*time.Second),
		PlayerCountTTL:    envDuration("PLAYER_COUNT_TTL", 10*time.Second),
		ReadyTimeout:      envDuration("READY_TIMEOUT", 90*time.Second),
	}
	for _, u := range strings.Split(env("RCC_RENDER_URL", "http://localhost:64989/"), ",") {
		u = strings.TrimSpace(u)
		if u != "" {
			c.RenderURLs = append(c.RenderURLs, u)
		}
	}

	var problems []string
	if c.Password == "" {
		problems = append(problems, "PASSWORD is empty")
	}
	if c.GameSecret == "" {
		problems = append(problems, "RCC_GAME_SECRET is empty")
	}
	if c.RenderSecret == "" {
		problems = append(problems, "RCC_RENDER_SECRET is empty")
	}
	if c.SiteInternalURL == "" {
		problems = append(problems, "SITE_INTERNAL_URL is empty")
	}
	if c.SitePublicAddress == "" {
		problems = append(problems, "SITE_PUBLIC_ADDRESS is empty")
	}
	// a render only box has no script
	if c.GameServerScript != "" {
		if _, err := os.Stat(c.GameServerScript); err != nil {
			problems = append(problems, "GAMESERVER_SCRIPT: "+err.Error())
		}
	}
	if c.PortMin < 1 || c.PortMax > 65535 || c.PortMin > c.PortMax {
		problems = append(problems, fmt.Sprintf("PORT_MIN..PORT_MAX %d..%d is not a valid range", c.PortMin, c.PortMax))
	}
	if len(problems) > 0 {
		return nil, errors.New("config: " + strings.Join(problems, "; "))
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
