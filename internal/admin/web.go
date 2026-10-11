package admin

import "embed"

//go:embed web/index.html web/dashboard.css web/dashboard.js
var webFiles embed.FS

type webAsset struct {
	contentType string
	body        []byte
}

var webAssets = map[string]webAsset{
	"/admin":                      embeddedAsset("web/index.html", "text/html; charset=utf-8"),
	"/admin/":                     embeddedAsset("web/index.html", "text/html; charset=utf-8"),
	"/admin/assets/dashboard.css": embeddedAsset("web/dashboard.css", "text/css; charset=utf-8"),
	"/admin/assets/dashboard.js":  embeddedAsset("web/dashboard.js", "text/javascript; charset=utf-8"),
}

func embeddedAsset(name, contentType string) webAsset {
	body, err := webFiles.ReadFile(name)
	if err != nil {
		panic("Missing embedded admin asset.")
	}
	return webAsset{contentType, body}
}
