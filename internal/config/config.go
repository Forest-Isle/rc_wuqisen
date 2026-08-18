package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL, APIToken, ListenAddr                                                        string
	Workers                                                                                  int
	PollInterval, LeaseDuration, RequestTimeout, ShutdownTimeout, InitialBackoff, MaxBackoff time.Duration
	MaxAttempts                                                                              int
	AllowHTTP, AllowPrivate                                                                  bool
	Allowlist                                                                                []string
	MaxBodyBytes                                                                             int64
}

func Load() (Config, error) {
	allowHTTP, err := boolEnv("ALLOW_HTTP", false)
	if err != nil {
		return Config{}, err
	}
	allowPrivate, err := boolEnv("ALLOW_PRIVATE_TARGETS", false)
	if err != nil {
		return Config{}, err
	}
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), APIToken: os.Getenv("API_TOKEN"), ListenAddr: env("LISTEN_ADDR", ":8080"), Workers: intEnv("WORKERS", 4), PollInterval: dur("POLL_INTERVAL", 500*time.Millisecond), LeaseDuration: dur("LEASE_DURATION", 30*time.Second), RequestTimeout: dur("REQUEST_TIMEOUT", 10*time.Second), ShutdownTimeout: dur("SHUTDOWN_TIMEOUT", 15*time.Second), InitialBackoff: dur("INITIAL_BACKOFF", 5*time.Second), MaxBackoff: dur("MAX_BACKOFF", 15*time.Minute), MaxAttempts: intEnv("MAX_ATTEMPTS", 8), AllowHTTP: allowHTTP, AllowPrivate: allowPrivate, MaxBodyBytes: int64(intEnv("MAX_BODY_BYTES", 262144))}
	for _, v := range strings.Split(os.Getenv("DESTINATION_ALLOWLIST"), ",") {
		if strings.TrimSpace(v) != "" {
			c.Allowlist = append(c.Allowlist, strings.ToLower(strings.TrimSpace(v)))
		}
	}
	if c.DatabaseURL == "" || c.APIToken == "" {
		return c, errors.New("DATABASE_URL and API_TOKEN are required")
	}
	if c.Workers < 1 || c.MaxAttempts < 1 || c.MaxBodyBytes < 1 || c.PollInterval <= 0 || c.LeaseDuration <= 0 || c.RequestTimeout <= 0 || c.ShutdownTimeout <= 0 || c.InitialBackoff <= 0 || c.MaxBackoff <= 0 || c.InitialBackoff > c.MaxBackoff || c.LeaseDuration <= c.RequestTimeout {
		return c, errors.New("invalid numeric configuration")
	}
	return c, nil
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func intEnv(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	n, e := strconv.Atoi(v)
	if e != nil {
		return -1
	}
	return n
}
func dur(k string, d time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	x, e := time.ParseDuration(v)
	if e != nil {
		return -1
	}
	return x
}
func boolEnv(k string, d bool) (bool, error) {
	v := os.Getenv(k)
	if v == "" {
		return d, nil
	}
	x, e := strconv.ParseBool(v)
	if e != nil {
		return false, errors.New(k + " must be true or false")
	}
	return x, nil
}
