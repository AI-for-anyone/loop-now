//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Windows 上 net.Listen 的"地址已被占用"是 Winsock 的 WSAEADDRINUSE。
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
