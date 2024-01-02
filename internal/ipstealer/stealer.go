package ipstealer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
	"golang.org/x/oauth2/clientcredentials"
	"tailscale.com/client/tailscale"
)

const (
	tailscaleAPIBase      = "https://api.tailscale.com"
	setDeviceIPv4Endpoint = "/api/v2/device/%s/ip"
)

var (
	errFailedToFindTargetDevice = errors.New("failed to find target device in Tailscale device list")
	errFailedToSetDeviceIP      = errors.New("API call to set device IP failed")
)

type PeriodicThief struct {
	ctx    context.Context
	logger *zap.Logger
	config *Config
	client *tailscale.Client
}

type Config struct {
	Tailnet        string `mapstructure:"tailnet"`
	ClientID       string `mapstructure:"client_id"`
	ClientSecret   string `mapstructure:"client_secret"`
	TargetHostname string `mapstructure:"target_hostname"`
	DesiredIP      string `mapstructure:"desired_ip"`
	PeriodSeconds  int    `mapstructure:"period_seconds"`
}

func New(ctx context.Context, logger *zap.Logger, config *Config) *PeriodicThief {
	oauthConfig := &clientcredentials.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		TokenURL:     "https://api.tailscale.com/api/v2/oauth/token",
	}

	// lol
	tailscale.I_Acknowledge_This_API_Is_Unstable = true

	oauthClient := oauthConfig.Client(ctx)

	client := tailscale.NewClient(config.Tailnet, nil)
	client.HTTPClient = oauthClient

	return &PeriodicThief{
		ctx:    ctx,
		logger: logger,
		client: client,
		config: config,
	}
}

func (p *PeriodicThief) Start() *time.Ticker {
	ticker := time.NewTicker(time.Duration(p.config.PeriodSeconds) * time.Second)
	go func() {
		for {
			select {
			case <-ticker.C:
				p.logger.Info("starting scheduled IP steal")
				err := p.Steal()
				if err != nil {
					p.logger.Error("failed to steal IP", zap.Error(err))
				}
			case <-p.ctx.Done():
				return
			}
		}
	}()

	return ticker
}

func (p *PeriodicThief) Steal() error {
	devices, err := p.client.Devices(p.ctx, tailscale.DeviceDefaultFields)
	if err != nil {
		return fmt.Errorf("failed to fetch list of devices: %w", err)
	}

	var occupiedIPs []string
	var currentDevice *tailscale.Device
	var targetDevice *tailscale.Device
	var targetDeviceLastSeen time.Time
	for _, device := range devices {
		for _, address := range device.Addresses {
			if address == p.config.DesiredIP {
				currentDevice = device
			}

			occupiedIPs = append(occupiedIPs, address)
		}

		if device.Hostname == p.config.TargetHostname {
			if device.LastSeen == "" {
				// N.B. safe to continue here, because we've done all we wanted
				// to do with the desired IP stuff above
				continue
			}

			lastSeen, err := time.Parse(time.RFC3339, device.LastSeen)
			if err != nil {
				return fmt.Errorf("saw unparsable last seen time '%s' in devices", lastSeen)
			}

			if targetDevice == nil || lastSeen.After(targetDeviceLastSeen) {
				targetDevice = device
				targetDeviceLastSeen = lastSeen
			}
		}
	}

	if targetDevice == nil {
		return errFailedToFindTargetDevice
	}

	if currentDevice == targetDevice {
		p.logger.Debug("target device has the desired IP; nothing to do")
		return nil
	} else if currentDevice != nil {
		p.logger.Info("device is occupying our desired IP; setting to random new IP",
			zap.String("deviceID", currentDevice.DeviceID),
			zap.String("name", currentDevice.Name),
		)

		err := p.setDeviceIPv4(currentDevice, randomTailscaleIPv4(occupiedIPs))
		if err != nil {
			return fmt.Errorf("failed to change currently occupying device's IP: %w", err)
		}
	}

	p.logger.Info("attempting to change target device to desired IP",
		zap.String("deviceID", targetDevice.DeviceID),
		zap.String("name", targetDevice.Name),
	)
	return p.setDeviceIPv4(targetDevice, p.config.DesiredIP)
}

func (p *PeriodicThief) setDeviceIPv4(device *tailscale.Device, ip string) error {
	req, err := makeSetDeviceIPv4Request(p.ctx, device.DeviceID, p.config.DesiredIP)
	if err != nil {
		return fmt.Errorf("failed to make set device IP request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("tailscale API call to change IP could not be made: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		p.logger.Error("obtained non-200 status from device IP change request",
			zap.Int("status", resp.StatusCode),
			zap.ByteString("body", body),
		)
		return errFailedToSetDeviceIP
	}

	return nil
}
