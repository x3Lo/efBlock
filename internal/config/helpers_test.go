package config

import "net"

func netIP(s string) net.IP { return net.ParseIP(s) }
