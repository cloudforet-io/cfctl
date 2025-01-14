package configs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func AddAlias(service, key, value string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")

	data, err := os.ReadFile(settingPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read config: %v", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	aliases, ok := config["aliases"].(map[string]interface{})
	if !ok {
		aliases = make(map[string]interface{})
	}

	serviceAliases, ok := aliases[service].(map[string]interface{})
	if !ok {
		serviceAliases = make(map[string]interface{})
	}

	serviceAliases[key] = value
	aliases[service] = serviceAliases

	delete(config, "aliases")

	newData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode config: %v", err)
	}

	aliasData, err := yaml.Marshal(map[string]interface{}{
		"aliases": aliases,
	})
	if err != nil {
		return fmt.Errorf("failed to encode aliases: %v", err)
	}

	finalData := append(newData, aliasData...)

	if err := os.WriteFile(settingPath, finalData, 0644); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func RemoveAlias(service, key string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")

	data, err := os.ReadFile(settingPath)
	if err != nil {
		return fmt.Errorf("failed to read config: %v", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %v", err)
	}

	aliases, ok := config["aliases"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no aliases found")
	}

	serviceAliases, ok := aliases[service].(map[string]interface{})
	if !ok {
		return fmt.Errorf("no aliases found for service '%s'", service)
	}

	if _, exists := serviceAliases[key]; !exists {
		return fmt.Errorf("alias '%s' not found in service '%s'", key, service)
	}

	delete(serviceAliases, key)
	if len(serviceAliases) == 0 {
		delete(aliases, service)
	} else {
		aliases[service] = serviceAliases
	}

	config["aliases"] = aliases

	newData, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to encode config: %v", err)
	}

	if err := os.WriteFile(settingPath, newData, 0644); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	return nil
}

func ListAliases() (map[string]interface{}, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	aliases := v.Get("aliases")
	if aliases == nil {
		return make(map[string]interface{}), nil
	}

	aliasesMap, ok := aliases.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid aliases format")
	}

	return aliasesMap, nil
}

func LoadAliases() (map[string]interface{}, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("unable to find home directory: %v", err)
	}

	settingPath := filepath.Join(home, ".cfctl", "setting.yaml")
	v := viper.New()
	v.SetConfigFile(settingPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		if os.IsNotExist(err) {
			return make(map[string]interface{}), nil
		}
		return nil, fmt.Errorf("failed to read config: %v", err)
	}

	aliases := v.Get("aliases")
	if aliases == nil {
		return make(map[string]interface{}), nil
	}

	aliasesMap, ok := aliases.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid aliases format")
	}

	return aliasesMap, nil
}
