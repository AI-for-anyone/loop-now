//go:build windows

package app

import (
	"errors"

	"golang.org/x/sys/windows"
)

// On Windows, "address already in use" from net.Listen is Winsock's WSAEADDRINUSE.
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
