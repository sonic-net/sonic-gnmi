package gnmi

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/sonic-net/sonic-gnmi/pkg/authzpolicy"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

func loadAuthzPolicy(config *Config) (*authzpolicy.PolicyInterceptor, error) {
	sourceName := config.AuthzPolicySource
	if sourceName == "" {
		sourceName = authzpolicy.SourceFile
	}

	var source authzpolicy.Source
	var closeSource func() error
	switch sourceName {
	case authzpolicy.SourceFile:
		source = authzpolicy.FileSource{
			Path:            config.AuthzPolicyFile,
			RefreshInterval: authzRefreshingInterval,
		}
	case authzpolicy.SourceConfigDB:
		var err error
		source, closeSource, err = newConfigDBAuthzPolicySource()
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported authorization policy source %q", sourceName)
	}

	interceptor, err := source.Load(context.Background())
	if closeSource != nil {
		if closeErr := closeSource(); err == nil && closeErr != nil {
			err = fmt.Errorf("close authorization policy source: %w", closeErr)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("load %s authorization policy: %w", sourceName, err)
	}
	return interceptor, nil
}

func newConfigDBAuthzPolicySource() (authzpolicy.Source, func() error, error) {
	namespace, err := sdcfg.GetDbDefaultNamespace()
	if err != nil {
		return nil, nil, fmt.Errorf("get CONFIG_DB namespace: %w", err)
	}
	dbID, err := sdcfg.GetDbId("CONFIG_DB", namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("get CONFIG_DB id: %w", err)
	}
	address, err := sdcfg.GetDbSock("CONFIG_DB", namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("get CONFIG_DB socket: %w", err)
	}
	separator, err := sdcfg.GetDbSeparator("CONFIG_DB", namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("get CONFIG_DB separator: %w", err)
	}
	client := redis.NewClient(&redis.Options{
		Network:     "unix",
		Addr:        address,
		DB:          dbID,
		DialTimeout: 0,
	})
	reader := authzpolicy.NewRedisTableReader(client, separator)
	return authzpolicy.NewConfigDBSource(reader), client.Close, nil
}
