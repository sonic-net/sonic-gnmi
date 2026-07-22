//go:build pure

package dbconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

const (
	defaultDatabaseConfigFile = ConfigFile
	defaultGlobalConfigFile   = GlobalConfigFile
)

var (
	databaseConfigFile          = defaultDatabaseConfigFile
	globalConfigFile            = defaultGlobalConfigFile
	activeProvider     provider = &fileProvider{}
)

type fileProvider struct {
	config databaseConfig
}

type databaseConfig struct {
	Instances map[string]instanceConfig `json:"INSTANCES"`
	Databases map[string]databaseEntry  `json:"DATABASES"`
	Includes  []json.RawMessage         `json:"INCLUDES"`
}

type instanceConfig struct {
	Hostname       string `json:"hostname"`
	Port           int    `json:"port"`
	UnixSocketPath string `json:"unix_socket_path"`
}

type databaseEntry struct {
	ID        int    `json:"id"`
	Separator string `json:"separator"`
	Instance  string `json:"instance"`
}

func (p *fileProvider) initialize() error {
	if _, err := os.Stat(globalConfigFile); err == nil {
		return fmt.Errorf("global database configuration is not supported by the pure provider")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect global database configuration: %w", err)
	}

	data, err := os.ReadFile(databaseConfigFile)
	if err != nil {
		return fmt.Errorf("read database configuration: %w", err)
	}
	if err := json.Unmarshal(data, &p.config); err != nil {
		return fmt.Errorf("parse database configuration: %w", err)
	}
	if len(p.config.Includes) > 0 {
		return fmt.Errorf("global database configuration is not supported by the pure provider")
	}
	return nil
}

func (p *fileProvider) reset() error {
	p.config = databaseConfig{}
	return nil
}

func (p *fileProvider) namespaces() ([]string, error) {
	return []string{DefaultNamespace}, nil
}

func (p *fileProvider) dbList(namespace string) ([]string, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(p.config.Databases))
	for name := range p.config.Databases {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func (p *fileProvider) dbID(name, namespace string) (int, error) {
	entry, _, err := p.lookup(name, namespace)
	if err != nil {
		return -1, err
	}
	return entry.ID, nil
}

func (p *fileProvider) dbSeparator(name, namespace string) (string, error) {
	entry, _, err := p.lookup(name, namespace)
	if err != nil {
		return "", err
	}
	return entry.Separator, nil
}

func (p *fileProvider) dbSocket(name, namespace string) (string, error) {
	_, instance, err := p.lookup(name, namespace)
	if err != nil {
		return "", err
	}
	return instance.UnixSocketPath, nil
}

func (p *fileProvider) dbHostname(name, namespace string) (string, error) {
	_, instance, err := p.lookup(name, namespace)
	if err != nil {
		return "", err
	}
	return instance.Hostname, nil
}

func (p *fileProvider) dbPort(name, namespace string) (int, error) {
	_, instance, err := p.lookup(name, namespace)
	if err != nil {
		return -1, err
	}
	return instance.Port, nil
}

func (p *fileProvider) lookup(name, namespace string) (databaseEntry, instanceConfig, error) {
	if err := validateNamespace(namespace); err != nil {
		return databaseEntry{}, instanceConfig{}, err
	}
	entry, ok := p.config.Databases[name]
	if !ok {
		return databaseEntry{}, instanceConfig{}, fmt.Errorf("database %q not present in standalone database configuration", name)
	}
	instance, ok := p.config.Instances[entry.Instance]
	if !ok {
		return databaseEntry{}, instanceConfig{}, fmt.Errorf("instance %q for database %q not present in standalone database configuration", entry.Instance, name)
	}
	return entry, instance, nil
}

func validateNamespace(namespace string) error {
	if namespace != DefaultNamespace {
		return fmt.Errorf("namespace %q not present in standalone database configuration", namespace)
	}
	return nil
}
