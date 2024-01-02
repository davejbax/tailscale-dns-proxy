package ipstealer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type setDeviceIPv4RequestBody struct {
	IPv4 string `json:"ipv4"`
}

func makeSetDeviceIPv4Request(ctx context.Context, deviceID string, ipv4 string) (*http.Request, error) {
	reqBody := setDeviceIPv4RequestBody{IPv4: ipv4}
	body, err := json.Marshal(reqBody)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON body: %v", err))
	}

	return http.NewRequestWithContext(
		ctx,
		"POST",
		tailscaleAPIBase+fmt.Sprintf(setDeviceIPv4Endpoint, deviceID),
		bytes.NewReader(body),
	)
}
