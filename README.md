# Tailscale DNS Proxy

[![CI status](https://github.com/davejbax/tailscale-dns-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/davejbax/tailscale-dns-proxy/actions?query=workflow:ci)
[![Go Report Card](https://goreportcard.com/badge/github.com/davejbax/tailscale-dns-proxy)](https://goreportcard.com/report/github.com/davejbax/tailscale-dns-proxy)

*Note: This project has no affiliation with [Tailscale](https://www.tailscale.com/)*

DNS server that rewrites responses containing private IPs corresponding to your Tailscale machines to their Tailnet IP addresses.

![Diagram showing how tailscale-dns-proxy rewrites '192.168.1.123' to a Tailnet IP](./docs/diagram.png)

## Usage

### Resolvers

## FAQ

### Why would anyone want this?

### Why not use a subnet router?

### Why not use a split-horizon DNS server?

### Why not use Tailscale IPs exclusively?

### Why not just assign your Tailnet IP to a different subdomain?

### Isn't returning private IP ranges bad because of DNS rebinding?