package configs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Setting represents the complete configuration structure
type Setting struct {
	Environment  string                 `yaml:"environment"`  // Current active environment
	Environments map[string]Environment `yaml:"environments"` // Map of available environments
}

// Environment represents a single environment configuration
type Environment struct {
	Endpoint string `yaml:"endpoint"` // gRPC or HTTP endpoint URL
	Proxy    string `yaml:"proxy"`    // Proxy server address if required
	Token    string `yaml:"token"`    // Authentication token
}

// LoadSetting loads the setting from the default location (~/.cfctl/setting.yaml)
// and handles environment-specific token loading.
func LoadSetting() (*Setting, error) {
	settingPath, err := GetSettingPath()
	if err != nil {
		return nil, err
	}

	setting, err := loadMainSetting(settingPath)
	if err != nil {
		return nil, err
	}

	envSetting, err := loadEnvironmentSetting(setting.Environment)
	if err != nil {
		return nil, err
	}

	return &Setting{
		Environment: setting.Environment,
		Environments: map[string]Environment{
			setting.Environment: *envSetting,
		},
	}, nil
}

// GetSettingPath returns the path to the main setting file
func GetSettingPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %v", err)
	}
	return filepath.Join(home, ".cfctl", "setting.yaml"), nil
}

// loadMainSetting loads the main setting file using viper
func loadMainSetting(settingPath string) (*Setting, error) {
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	currentEnv := v.GetString("environment")
	if currentEnv == "" {
		return nil, fmt.Errorf("no environment set in settings.yaml")
	}

	return &Setting{Environment: currentEnv}, nil
}

// loadEnvironmentSetting loads environment-specific setting
func loadEnvironmentSetting(env string) (*Environment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	v := viper.New()
	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read settings.yaml: %v", err)
	}

	envSetting := &Environment{
		Endpoint: v.GetString(fmt.Sprintf("environments.%s.endpoint", env)),
		Proxy:    v.GetString(fmt.Sprintf("environments.%s.proxy", env)),
	}

	if err := loadToken(env, envSetting); err != nil {
		return nil, err
	}

	return envSetting, nil
}

// loadToken loads the appropriate token based on environment type
func loadToken(env string, envSetting *Environment) error {
	if strings.HasSuffix(env, "-user") {
		return loadUserToken(env, envSetting)
	}
	return loadAppToken(env, envSetting)
}

// loadUserToken loads token for user environments from access_token file
func loadUserToken(env string, envSetting *Environment) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	tokenPath := filepath.Join(home, ".cfctl", "cache", env, "access_token")
	tokenBytes, err := os.ReadFile(tokenPath)
	if err == nil {
		envSetting.Token = strings.TrimSpace(string(tokenBytes))
	}
	return nil
}

// loadAppToken loads token for app environments from main setting
func loadAppToken(env string, envSetting *Environment) error {
	v := viper.New()
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("failed to read setting file: %v", err)
	}

	envSetting.Token = v.GetString(fmt.Sprintf("environments.%s.token", env))
	return nil
}
