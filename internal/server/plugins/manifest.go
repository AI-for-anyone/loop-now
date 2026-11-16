// Package plugins implements install, config, enabling, grants and accounts for the second version of plugins
// (plugins/SPEC.md §2, §5, §6). It provides the plugin host supervisor with the plugins to load and assembles the
// agent-side parameters for Pi. Reading and validating manifests lives in pluginmanifest.
package plugins

import "loop-now/internal/pluginmanifest"

// The manifest types are taken from pluginmanifest.
type (
	Manifest       = pluginmanifest.Manifest
	Text           = pluginmanifest.Text
	PublicManifest = pluginmanifest.PublicManifest
	AccountKind    = pluginmanifest.AccountKind
	Action         = pluginmanifest.Action
	Channel        = pluginmanifest.Channel
	Schedule       = pluginmanifest.Schedule
)

// ManifestFile is the only manifest file in a plugin directory.
const ManifestFile = pluginmanifest.ManifestFile

// ReadManifest reads and validates the manifest of a plugin directory; when it breaks the rules it returns a 422 on
// the manifest field.
func ReadManifest(dir, platformVersion string) (Manifest, error) {
	m, err := pluginmanifest.ReadManifest(dir, platformVersion)
	return m, asInvalid(err, "manifest")
}
