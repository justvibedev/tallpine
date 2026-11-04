package controllers

// SaveRenderArtifactRequest keeps a published render as a session artifact.
type SaveRenderArtifactRequest struct {
	Title string `json:"title" minLength:"1" maxLength:"200" description:"Name for the file. Characters a file system refuses are replaced."`
}

// SaveRenderArtifactResponse names the file the render was kept as.
type SaveRenderArtifactResponse struct {
	Path string `json:"path" description:"The file, relative to the session's artifact directory."`
	Name string `json:"name"`
}

// RenderCheckScreenshot is the page as the desktop app drew it.
type RenderCheckScreenshot struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data" description:"Base64 PNG."`
	Width    int    `json:"width" description:"Page width the screenshot shows, in CSS pixels."`
	Height   int    `json:"height" description:"Page height the screenshot shows, in CSS pixels."`
	// The desktop app scales a very large image down so it can be handed back.
	ImageWidth  int `json:"imageWidth,omitempty" description:"PNG width in pixels, when it differs from the page width."`
	ImageHeight int `json:"imageHeight,omitempty" description:"PNG height in pixels, when it differs from the page height."`
}

// RenderConsoleMessage is one console line the page wrote while it loaded.
type RenderConsoleMessage struct {
	Level string `json:"level" enum:"debug,log,warning,error"`
	Text  string `json:"text"`
}

// RenderCheckResponse reports how the page rendered.
type RenderCheckResponse struct {
	Screenshot      RenderCheckScreenshot  `json:"screenshot"`
	ContentHeight   int                    `json:"contentHeight" description:"Height the page needs at this width, in CSS pixels."`
	ConsoleMessages []RenderConsoleMessage `json:"consoleMessages"`
	Network         string                 `json:"network" enum:"public,none" description:"Network the page could use. none when the agent's own sandbox has no network, so remote resources did not load."`
}
