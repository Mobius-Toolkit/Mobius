package api

import (
	"slices"
	"strings"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
)

// validateImages checks the type of each image of a request.
func validateImages(images []api.File) error {
	for _, image := range images {
		if !slices.Contains(imageTypes, image.ContentType) {
			return &api.BodyValidationError{Errors: []string{"an image must be PNG, JPEG, GIF or WebP"}}
		}
	}
	return nil
}

// unixLines changes each CRLF of text to LF. A browser sends each new line of a multipart text field as CRLF.
func unixLines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// attachmentsOf gives the parts of a multipart request for the engine.
func attachmentsOf(images []api.File, inboxItems []ChatInboxItem, selections []ChatSelection, voice bool) engine.Attachments {
	attachments := engine.Attachments{
		Images:     make([]engine.Image, 0, len(images)),
		InboxItems: make([]engine.ChatInboxItem, 0, len(inboxItems)),
		Selections: make([]engine.ChatSelection, 0, len(selections)),
		Voice:      voice,
	}
	for _, image := range images {
		attachments.Images = append(attachments.Images, engine.Image{MIMEType: image.ContentType, Data: image.Data})
	}
	for _, item := range inboxItems {
		attachments.InboxItems = append(attachments.InboxItems, engine.ChatInboxItem(item))
	}
	for _, selection := range selections {
		attachments.Selections = append(attachments.Selections, engine.ChatSelection(selection))
	}
	return attachments
}
