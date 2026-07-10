package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// ShitlistConfig holds the configuration for which shitlist categories are allowed.
type ShitlistConfig struct {
	AllowBuyMugger         bool
	AllowAbsoluteScumLords bool
	AllowOther             bool
}

// LoadShitlistConfig parses the shitlist.env file and personalSList.txt from the user's config directory.
func LoadShitlistConfig() (*ShitlistConfig, error) {
	dir, err := GetUserDir()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve user directory: %w", err)
	}
	envPath := filepath.Join(dir, "shitlist.env")

	// Attempt to load the shitlist.env file.
	envMap, err := godotenv.Read(envPath)
	if err != nil {
		envMap = make(map[string]string) // Fallback if file is missing
	}

	resolveBool := func(key string) bool {
		val := envMap[key]
		if val == "" {
			val = os.Getenv(key)
		}
		return strings.ToLower(val) == "true"
	}

	return &ShitlistConfig{
		AllowBuyMugger:         resolveBool("ALLOW_BUY_MUGGER"),
		AllowAbsoluteScumLords: resolveBool("ALLOW_ABSOLUTE_SCUM_LORDS"),
		AllowOther:             resolveBool("ALLOW_OTHER"),
	}, nil
}
