// Package portlessweb exposes the production control-plane assets embedded in
// the Portless executable.
package portlessweb

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

func Assets() fs.FS {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}
