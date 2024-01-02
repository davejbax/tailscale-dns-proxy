package ipstealer

import (
	"fmt"
	"math/rand"
)

func randomTailscaleIPv4(occupiedIPs []string) string {
regenerate:
	for {
		randomIP := fmt.Sprintf("100.64.%d.%d", rand.Intn(256), rand.Intn(256))
		for _, ip := range occupiedIPs {
			if ip == randomIP {
				continue regenerate
			}
		}
		return randomIP
	}
}
