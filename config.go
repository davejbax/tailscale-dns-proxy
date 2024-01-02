package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/davejbax/tailscale-dns-proxy/internal/ipstealer"
	"github.com/davejbax/tailscale-dns-proxy/internal/proxy"
	"github.com/davejbax/tailscale-dns-proxy/internal/resolvers"
	"github.com/go-playground/validator/v10"
	"github.com/spf13/viper"
)

var errNoResolvers = errors.New("no resolvers specified in resolver config")

const (
	envPrefix = "TSDNSPROXY"
)

type AppConfig struct {
	Proxy     proxy.Config `mapstructure:"proxy"`
	IPStealer struct {
		Enabled          bool `mapstructure:"enabled"`
		ipstealer.Config `mapstructure:",squash" validate:"required_if=Enabled true"`
	}
	Resolver ResolverConfig `mapstructure:"resolver"`
}

type ResolverConfig struct {
	StartTimeoutSeconds int                         `mapstructure:"start_timeout_seconds"`
	Kubernetes          *resolvers.KubernetesConfig `mapstructure:"kubernetes"`
}

func (r *ResolverConfig) Create() (resolvers.Resolver, error) {
	switch {
	case r.Kubernetes != nil:
		return resolvers.NewKubernetesResolverWithDefaultClient(r.Kubernetes)
	default:
		return nil, errNoResolvers
	}
}

func loadConfig() (*AppConfig, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/tsdnsproxy")
	viper.AddConfigPath(".")
	viper.SetEnvPrefix(envPrefix)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "__")) // Converts Viper keys into env var keys

	// TODO: replace this with viper.BindStruct once released and stable;
	// see https://github.com/spf13/viper/issues/1706
	// and https://github.com/spf13/viper/pull/1707
	// and https://github.com/spf13/viper/issues/761
	for _, e := range os.Environ() {
		split := strings.Split(e, "=")
		envVariable := split[0]

		// Trim prefix and only proceed if we successfully trimmed it (i.e. skip non-prefixed vars)
		if envKey := strings.TrimPrefix(envVariable, envPrefix+"_"); envKey != envVariable {
			viper.BindEnv(strings.ReplaceAll(envKey, "__", "."))
		}
	}

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		// We don't care about the config not being found, because it's theoretically
		// possible to configure entirely with env vars.
	}

	var config AppConfig
	err := viper.Unmarshal(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	validate := validator.New()
	if err := validate.Struct(config); err != nil {
		return nil, fmt.Errorf("config is invalid: %w", err)
	}

	return &config, nil
}
