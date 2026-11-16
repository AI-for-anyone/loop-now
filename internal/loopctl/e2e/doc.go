// Package e2e holds the end-to-end tests of loopctl against the real server (tasks sections 9 and 10); it contains
// only test code.
// The tests start the server in-process with a temporary data directory and fakepi, and never connect to a
// development server running on this machine.
package e2e
