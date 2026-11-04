package controllers

import (
	"encoding/base64"
	"mime"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// blockedAttachmentMimes contains MIME types that are explicitly rejected for
// security reasons. SVG is excluded because it is XML that can carry active
// content (scripts/external entities).
var blockedAttachmentMimes = map[string]bool{
	"image/svg+xml": true,
}

// attachmentError carries a client-facing API error code + message for a
// rejected attachment payload.
type attachmentError struct {
	code    string
	message string
}

// extensionForMimeType returns a file extension for a MIME type. For known
// MIME types, it returns a standard extension. For unknown types, it attempts
// to extract a reasonable extension from the MIME type, or falls back to ".bin".
func extensionForMimeType(mimeType string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))

	// Preferred extensions for MIME types with multiple options
	preferredExts := map[string]string{
		"image/jpeg":      ".jpg",
		"image/jpg":       ".jpg",
		"text/plain":      ".txt",
		"video/quicktime": ".mov",
	}

	// Check if we have a preferred extension for this MIME type
	if pref, ok := preferredExts[mimeType]; ok {
		return pref
	}

	// Try the standard mime package
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		// Prefer common extensions when available
		for _, ext := range exts {
			switch ext {
			case ".jpg", ".jpeg", ".png", ".gif", ".pdf", ".txt", ".json", ".xml", ".html", ".css", ".js", ".md", ".zip", ".tar", ".gz":
				return ext
			}
		}
		return exts[0] // Return the primary extension if no preferred match
	}

	// Fallback: extract from the MIME type itself
	// e.g., "application/pdf" -> ".pdf", "text/plain" -> ".plain"
	parts := strings.SplitN(mimeType, "/", 2)
	if len(parts) == 2 {
		subtype := parts[1]
		// Handle common suffixes like "+xml", "+json"
		if idx := strings.Index(subtype, "+"); idx >= 0 {
			subtype = subtype[:idx]
		}
		return "." + subtype
	}

	// Ultimate fallback for unknown MIME types
	return ".bin"
}

// decodeAttachment validates and base64-decodes a single inline file
// attachment shared by spawn, delegate, stage, send, and chat requests, enforcing
// the blocked-MIME-type rule and the caller's per-file cap. Callers handling multiple
// attachments are responsible for the count and total-size caps.
func decodeAttachment(a AttachmentInput, maxBytes int) (ports.SpawnAttachment, *attachmentError) {
	mimeType := strings.ToLower(strings.TrimSpace(a.MimeType))
	if blockedAttachmentMimes[mimeType] {
		return ports.SpawnAttachment{}, &attachmentError{"UNSUPPORTED_ATTACHMENT_TYPE", "unsupported attachment type"}
	}
	ext := extensionForMimeType(mimeType)
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.Data))
	if err != nil {
		return ports.SpawnAttachment{}, &attachmentError{"INVALID_ATTACHMENT_DATA", "attachment data is not valid base64"}
	}
	if len(data) == 0 {
		return ports.SpawnAttachment{}, &attachmentError{"INVALID_ATTACHMENT_DATA", "attachment is empty"}
	}
	if len(data) > maxBytes {
		return ports.SpawnAttachment{}, &attachmentError{"ATTACHMENT_TOO_LARGE", "attachment is too large"}
	}
	return ports.SpawnAttachment{Ext: ext, Data: data}, nil
}

func decodeSpawnAttachments(in []AttachmentInput) ([]ports.SpawnAttachment, *attachmentError) {
	return decodeAttachments(in, maxAttachmentBytes, maxAttachmentsBytes)
}

// decodeAttachments enforces the caller's count and size caps, accepting any
// MIME type except explicitly blocked ones (e.g., SVG).
func decodeAttachments(in []AttachmentInput, maxFileBytes, maxTotalBytes int) ([]ports.SpawnAttachment, *attachmentError) {
	if len(in) == 0 {
		return nil, nil
	}
	if len(in) > maxAttachments {
		return nil, &attachmentError{"TOO_MANY_ATTACHMENTS", "too many attachments"}
	}
	out := make([]ports.SpawnAttachment, 0, len(in))
	total := 0
	for _, a := range in {
		attachment, err := decodeAttachment(a, maxFileBytes)
		if err != nil {
			return nil, err
		}
		total += len(attachment.Data)
		if total > maxTotalBytes {
			return nil, &attachmentError{"ATTACHMENTS_TOO_LARGE", "attachments are too large"}
		}
		out = append(out, attachment)
	}
	return out, nil
}
