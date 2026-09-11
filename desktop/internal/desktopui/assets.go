package desktopui

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var embeddedAssets embed.FS

func BundledAssets() fs.FS {
	assets, e := fs.Sub(embeddedAssets, "assets/ui")
	if e != nil {
		panic(e)
	}
	return assets
}
