package egress

import (
	"net"
	"strings"
	"time"
)

func net_SplitHostPort(hostPort string) (string, string, error) {
	i := strings.LastIndex(hostPort, ":")
	if i < 0 {
		return hostPort, "", nil
	}
	return hostPort[:i], hostPort[i+1:], nil
}

func dialTCP(hostPort string) net.Conn {
	if _, _, err := net_SplitHostPort(hostPort); err != nil {
		hostPort += ":443"
	}
	conn, err := net.DialTimeout("tcp", hostPort, 10*time.Second)
	if err != nil {
		return nil
	}
	return conn
}
