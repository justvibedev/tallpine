package apitypes

// PublishRenderRequest is a self-contained HTML page an agent shows inline in
// its chat thread.
type PublishRenderRequest struct {
	HTML   string `json:"html" description:"A complete, self-contained HTML document, at most 25 MiB."`
	Title  string `json:"title" description:"Short name for the page."`
	Height int    `json:"height,omitempty" description:"First-paint frame height in CSS pixels, clamped to 80-2000; the frame then fits the page."`
	// Artifact keeps the page as a session artifact too. A failed save does not fail the publish.
	Artifact bool `json:"artifact,omitempty" description:"Also keep the page as a session artifact, a deliverable the user keeps."`
}

// PublishRenderResponse names the stored page and the timeline row showing it.
type PublishRenderResponse struct {
	RenderID      string `json:"renderId"`
	ActivityID    string `json:"activityId"`
	Path          string `json:"path"`
	ArtifactPath  string `json:"artifactPath,omitempty" description:"With artifact: the absolute path of the kept page."`
	ArtifactError string `json:"artifactError,omitempty" description:"With artifact: why the page was not kept. The page is still published."`
}

// RenderCheckRequest is a page an agent wants to see before it publishes it.
type RenderCheckRequest struct {
	HTML  string `json:"html" description:"A complete, self-contained HTML document, at most 25 MiB."`
	Width int    `json:"width,omitempty" description:"Viewport width in CSS pixels, 240-1600. Defaults to 720."`
}
