package proxy

type Config struct {
	ListenAddr                  string   `mapstructure:"listen_addr" validate:"required"`
	Upstreams                   []string `mapstructure:"upstreams" validate:"required"`
	UpstreamDialTimeoutSeconds  int      `mapstructure:"upstream_dial_timeout_seconds"`
	UpstreamReadTimeoutSeconds  int      `mapstructure:"upstream_read_timeout_seconds"`
	UpstreamWriteTimeoutSeconds int      `mapstructure:"upstream_write_timeout_seconds"`
	UpstreamTotalTimeoutSeconds int      `mapstructure:"upstream_total_timeout_seconds"`
	ProxyZones                  []string `mapstructure:"proxy_zones"`
}
