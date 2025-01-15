package configs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Environments represents the complete configuration structure
type Environments struct {
	Environment  string                 `yaml:"environment"`  // Current active environment
	Environments map[string]Environment `yaml:"environments"` // Map of available environments
}

// Environment represents a single environment configuration
type Environment struct {
	Endpoint string `yaml:"endpoint"` // gRPC or HTTP endpoint URL
	Proxy    string `yaml:"proxy"`    // Proxy server address if required
	Token    string `yaml:"token"`    // Authentication token
}

// SetSettingFile loads the setting from the default location (~/.cfctl/setting.yaml)
func SetSettingFile() (*Environments, error) {
	settingPath, err := GetSettingFilePath()
	if err != nil {
		return nil, err
	}

	currentEnvName, err := getCurrentEnvName(settingPath)
	if err != nil {
		return nil, err
	}

	currentEnvValues, err := getCurrentEnvValues(currentEnvName.Environment)
	if err != nil {
		return nil, err
	}

	return &Environments{
		Environment: currentEnvName.Environment,
		Environments: map[string]Environment{
			currentEnvName.Environment: *currentEnvValues,
		},
	}, nil
}

// GetSettingFilePath returns the path to the setting file in the .cfctl directory
func GetSettingFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %v", err)
	}

	return filepath.Join(home, ".cfctl", "setting.yaml"), nil
}

// getCurrentEnvName loads the main setting file using viper
func getCurrentEnvName(settingPath string) (*Environments, error) {
	v, err := setViperWithSetting(settingPath)
	if err != nil {
		return nil, err
	}

	currentEnv := v.GetString("environment")
	if currentEnv == "" {
		return nil, fmt.Errorf("no environment set in settings.yaml")
	}

	return &Environments{Environment: currentEnv}, nil
}

// getCurrentEnvValues loads environment-specific setting
func getCurrentEnvValues(env string) (*Environment, error) {
	settingPath, err := GetSettingFilePath()
	if err != nil {
		return nil, err
	}

	v, err := setViperWithSetting(settingPath)
	if err != nil {
		return nil, err
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
	settingPath, err := GetSettingFilePath()
	if err != nil {
		return err
	}

	v, err := setViperWithSetting(settingPath)
	if err != nil {
		return err
	}

	envSetting.Token = v.GetString(fmt.Sprintf("environments.%s.token", env))

	return nil
}

// setViperWithSetting creates a new viper instance with the given config file
func setViperWithSetting(settingPath string) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	return v, nil
}
