package service

import (
	"encoding/base32"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pborman/uuid"
)

var encoding = base32.NewEncoding("ybndrfg8ejkmcpqxot1uwisza345h769").WithPadding(base32.NoPadding)

func newID() string {
	return encoding.EncodeToString(uuid.NewRandom())
}

func parseAddress(addr string) (string, int, error) {
	if strings.Contains(addr, ":") {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return "", 0, err
		}

		p, err := strconv.Atoi(port)
		if err != nil {
			return "", 0, err
		}

		if p < minValidPortNum || p > maxValidPortNum {
			return "", 0, fmt.Errorf("port number not in valid range")
		}

		return host, p, nil
	}
	return addr, 0, nil
}
