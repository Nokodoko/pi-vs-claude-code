package util

import (
	"os"
	"strconv"
)

// EnvOr returns the value of key if non-empty, else def.
func EnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt returns the integer value of key if set and parseable, else def.
func EnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// MustGetwd returns the current working directory or "/tmp" on failure.
func MustGetwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "/tmp"
	}
	return d
}
