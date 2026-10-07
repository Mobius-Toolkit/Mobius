package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gork-labs/gork/pkg/api"

	"github.com/Mobius-Toolkit/Mobius/internal/engine"
	"github.com/Mobius-Toolkit/Mobius/internal/store"
)

// imageTypes are the MIME types of the images of a message.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// ChatMessage is a message of a Lead chat or of the Triager chat.
type ChatMessage struct {
	// ID increases with each new message of all chats
	ID int64 `gork:"id"`
	// Organization is the owner of the repository, or the organization of the Triager chat
	Organization string `gork:"organization"`
	// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
	Workstream int64 `gork:"workstream"`
	// Author is Owner, Lead, Triager, tell_owner for a message of the Lead to the Owner with an Inbox item, Mobius for
	// a message of Mobius, or Event for a Lead event, which the chat shows as a muted entry
	Author string `gork:"author" validate:"oneof=Owner Lead tell_owner Triager Mobius Event"`
	// Time is the time of the message
	Time time.Time `gork:"time"`
	// Text is the text of the message
	Text string `gork:"text"`
	// Images is the number of images of the message. GetChatImage gives each image by its position, from 0
	Images int64 `gork:"images"`
}

// Chat is a Lead chat or the Triager chat with its messages.
type Chat struct {
	// Messages are the messages, the oldest first
	Messages []ChatMessage `gork:"messages"`
	// Writing is true while the agent has a turn that runs or a message that waits
	Writing bool `gork:"writing"`
	// Harness is the Harness of the agent of the chat, for example claude-code
	Harness string `gork:"harness"`
}

// GetChatRequest is the request of GetChat.
type GetChatRequest struct {
	Query struct {
		// Organization is the owner of the repository, or the organization of the Triager chat
		Organization string `gork:"organization"`
		// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
		Repository string `gork:"repository"`
		// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
		Workstream int64 `gork:"workstream"`
	}
}

// GetChatResponse is the response of GetChat.
type GetChatResponse struct {
	Body Envelope[Chat]
}

// GetChat returns the Lead chat of a Workstream, or the Triager chat of an organization.
func (h *handlers) GetChat(ctx context.Context, req GetChatRequest) (*GetChatResponse, error) {
	view, err := h.engine.ChatView(ctx, engine.ChatKey(req.Query))
	if err != nil {
		return nil, err
	}
	chat := Chat{Messages: make([]ChatMessage, 0, len(view.Messages)), Writing: view.Writing, Harness: string(view.Harness)}
	for _, message := range view.Messages {
		found, err := h.chatMessageOf(message)
		if err != nil {
			return nil, err
		}
		chat.Messages = append(chat.Messages, found)
	}
	return &GetChatResponse{Body: Envelope[Chat]{Data: chat}}, nil
}

// SendChatRequest is the request of SendChat.
type SendChatRequest struct {
	Body struct {
		// Organization is the owner of the repository, or the organization of the Triager chat
		Organization string `gork:"organization"`
		// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
		Repository string `gork:"repository"`
		// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
		Workstream int64 `gork:"workstream"`
		// Text is the message of the Owner. It is required when the message has no image
		Text string `gork:"text" validate:"required_without=Images"`
		// Images are the images of the message, PNG, JPEG, GIF or WebP, each at most 5 MB
		Images []api.File `gork:"images" validate:"max=4,dive,max=5242880"`
	}
}

// Validate checks the type of each image.
func (r *SendChatRequest) Validate() error {
	for _, image := range r.Body.Images {
		if !slices.Contains(imageTypes, image.ContentType) {
			return &api.BodyValidationError{Errors: []string{"an image must be PNG, JPEG, GIF or WebP"}}
		}
	}
	return nil
}

// SendChat adds a message of the Owner with its images to the chat, and gives it to the Lead or to the Triager. Mobius
// starts the agent when none runs. It returns 400 when the request is larger than 4 images of 5 MB with the text. It
// returns 409 when the organization has no repository of Mobius, or while Mobius restarts for an upgrade.
func (h *handlers) SendChat(ctx context.Context, req SendChatRequest) error {
	body := req.Body
	// A browser sends each new line of a multipart text field as CRLF.
	text := strings.ReplaceAll(body.Text, "\r\n", "\n")
	images := make([]engine.Image, 0, len(body.Images))
	for _, image := range body.Images {
		images = append(images, engine.Image{MIMEType: image.ContentType, Data: image.Data})
	}
	err := h.engine.SendChat(ctx, engine.ChatKey{Organization: body.Organization, Repository: body.Repository, Workstream: body.Workstream}, text, images)
	if engine.Refused(err) {
		return api.NewHTTPError(http.StatusConflict, err.Error())
	}
	return err
}

// GetChatImageRequest is the request of GetChatImage.
type GetChatImageRequest struct {
	Path struct {
		// ID is the id of the message
		ID int64 `gork:"id"`
		// Position is the position of the image in the message, from 0
		Position int64 `gork:"position"`
	}
}

// GetChatImageResponse is the response of GetChatImage.
type GetChatImageResponse struct {
	Body api.Binary
}

// GetChatImage returns an image of a message. It returns 404 when the message has no such image.
func (h *handlers) GetChatImage(_ context.Context, req GetChatImageRequest) (*GetChatImageResponse, error) {
	image, err := h.engine.MessageImage(req.Path.ID, int(req.Path.Position))
	if err != nil {
		return nil, err
	}
	if image == nil {
		return nil, api.NewHTTPError(http.StatusNotFound, "The message has no such image.")
	}
	return &GetChatImageResponse{Body: api.Binary{ContentType: image.MIMEType, Data: image.Data}}, nil
}

// StopChatRequest is the request of StopChat.
type StopChatRequest struct {
	Body struct {
		// Organization is the owner of the repository, or the organization of the Triager chat
		Organization string `gork:"organization"`
		// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
		Repository string `gork:"repository"`
		// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
		Workstream int64 `gork:"workstream"`
	}
}

// StopChat stops the turn of the agent that answers a message of the Owner. A turn for a Lead event goes on. When the
// agent waits for its slot, its first message of the Owner gets no turn.
func (h *handlers) StopChat(ctx context.Context, req StopChatRequest) error {
	return h.engine.StopChat(ctx, engine.ChatKey(req.Body))
}

// SeeChatRequest is the request of SeeChat.
type SeeChatRequest struct {
	Body struct {
		// Organization is the owner of the repository, or the organization of the Triager chat
		Organization string `gork:"organization"`
		// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
		Repository string `gork:"repository"`
		// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
		Workstream int64 `gork:"workstream"`
		// Message is the id of the last message that the Owner saw
		Message int64 `gork:"message"`
	}
}

// SeeChat records that the Owner saw the messages of the chat up to a message. An earlier message than the last
// seen message changes nothing.
func (h *handlers) SeeChat(ctx context.Context, req SeeChatRequest) error {
	body := req.Body
	return h.engine.SeeChat(ctx, engine.ChatKey{Organization: body.Organization, Repository: body.Repository, Workstream: body.Workstream}, body.Message)
}

// Unread is the number of unread messages of a chat. The messages of the Owner and the Lead events are never unread.
type Unread struct {
	// Organization is the owner of the repository, or the organization of the Triager chat
	Organization string `gork:"organization"`
	// Repository is the repository of the Workstream as "owner/name". It is empty for the Triager chat
	Repository string `gork:"repository"`
	// Workstream is the number of the Workstream issue. It is 0 for the Triager chat
	Workstream int64 `gork:"workstream"`
	// Count is the number of unread messages
	Count int64 `gork:"count"`
}

// ListUnreadRequest is the request of ListUnread.
type ListUnreadRequest struct{}

// ListUnreadResponse is the response of ListUnread.
type ListUnreadResponse struct {
	Body Envelope[[]Unread]
}

// ListUnread returns each chat with unread messages.
func (h *handlers) ListUnread(ctx context.Context, _ ListUnreadRequest) (*ListUnreadResponse, error) {
	found, err := h.engine.UnreadChats(ctx)
	if err != nil {
		return nil, err
	}
	unread := make([]Unread, 0, len(found))
	for _, chat := range found {
		unread = append(unread, unreadOf(chat))
	}
	return &ListUnreadResponse{Body: Envelope[[]Unread]{Data: unread}}, nil
}

func unreadOf(unread engine.Unread) Unread {
	return Unread{Organization: unread.Key.Organization, Repository: unread.Key.Repository, Workstream: unread.Key.Workstream, Count: unread.Count}
}

func (h *handlers) chatMessageOf(message store.ChatMessage) (ChatMessage, error) {
	t, err := time.Parse(time.RFC3339Nano, message.Time)
	if err != nil {
		return ChatMessage{}, err
	}
	images, err := h.engine.ImageCount(message.ID)
	return ChatMessage{
		ID:           message.ID,
		Organization: message.Organization,
		Repository:   message.Repository,
		Workstream:   message.Workstream,
		Author:       message.Author,
		Time:         t,
		Text:         message.Text,
		Images:       int64(images),
	}, err
}
