package common

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// ResolveShortName checks if the given verb is a short name and returns the actual command if it is
func ResolveShortName(service, verb string) (string, string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.toml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("toml")

	if err := v.ReadInConfig(); err != nil {
		return "", "", false
	}

	// Check if there are short names for this service
	serviceShortNames := v.GetStringMapString(fmt.Sprintf("short_names.%s", service))
	if serviceShortNames == nil {
		return "", "", false
	}

	// Check if the verb is a short name
	if command, exists := serviceShortNames[verb]; exists {
		parts := strings.SplitN(command, " ", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], true
		}
	}

	return "", "", false
} 
