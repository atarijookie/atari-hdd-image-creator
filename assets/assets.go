// Package assets embeds the driver binaries and their boot loaders.
package assets

import _ "embed"

// DriversZip is built by tools/extract_loaders.py.
//
//go:embed drivers.zip
var DriversZip []byte
