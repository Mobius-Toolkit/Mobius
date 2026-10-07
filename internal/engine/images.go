package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Mobius-Toolkit/Mobius/internal/runner"
)

// Image is an image of a message of the Owner.
type Image = runner.Image

// imageTypes are the MIME types of the images that a message can have.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

// imagesDir gives the directory of the images of the chat message. A file of the directory has the position of its
// image as its name and the subtype of its MIME type as its extension, for example 0.png.
func imagesDir(dataDir string, message int64) string {
	return filepath.Join(dataDir, "images", strconv.FormatInt(message, 10))
}

// saveImages writes images to a new directory and gives its path. The caller moves the directory with moveImages, or
// removes it.
func (e *Engine) saveImages(images []Image) (string, error) {
	if err := os.MkdirAll(filepath.Join(e.config.DataDir, "images"), 0o750); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(filepath.Join(e.config.DataDir, "images"), "new-")
	if err != nil {
		return "", err
	}
	for position, image := range images {
		if !slices.Contains(imageTypes, image.MIMEType) {
			_ = os.RemoveAll(dir)
			return "", refuse("An image must be PNG, JPEG, GIF or WebP.")
		}
		name := strconv.Itoa(position) + "." + strings.TrimPrefix(image.MIMEType, "image/")
		if err := os.WriteFile(filepath.Join(dir, name), image.Data, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// moveImages makes the directory dir of saveImages the images of the chat message.
func (e *Engine) moveImages(dir string, message int64) error {
	return os.Rename(dir, imagesDir(e.config.DataDir, message))
}

// imageNames gives the file names of the images of the chat message, in the order of their positions.
func (e *Engine) imageNames(message int64) ([]string, error) {
	entries, err := os.ReadDir(imagesDir(e.config.DataDir, message))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	position := func(name string) int {
		n, _ := strconv.Atoi(strings.TrimSuffix(name, filepath.Ext(name)))
		return n
	}
	slices.SortFunc(names, func(a, b string) int { return position(a) - position(b) })
	return names, nil
}

func (e *Engine) readImage(message int64, name string) (Image, error) {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(imagesDir(e.config.DataDir, message), name)))
	if err != nil {
		return Image{}, err
	}
	return Image{MIMEType: "image/" + strings.TrimPrefix(filepath.Ext(name), "."), Data: data}, nil
}

// ImageCount gives the number of images of the chat message.
func (e *Engine) ImageCount(message int64) (int, error) {
	names, err := e.imageNames(message)
	return len(names), err
}

// MessageImage gives the image at position of the chat message. It gives nil when the message has no such image.
func (e *Engine) MessageImage(message int64, position int) (*Image, error) {
	names, err := e.imageNames(message)
	if err != nil || position < 0 || position >= len(names) {
		return nil, err
	}
	image, err := e.readImage(message, names[position])
	return &image, err
}

// messageImages gives the images of the chat message, in the order of their positions.
func (e *Engine) messageImages(message int64) ([]Image, error) {
	names, err := e.imageNames(message)
	if err != nil {
		return nil, err
	}
	images := make([]Image, 0, len(names))
	for _, name := range names {
		image, err := e.readImage(message, name)
		if err != nil {
			return nil, err
		}
		images = append(images, image)
	}
	return images, nil
}

// imagesText gives the count of images as text, for example "1 image".
func imagesText(count int) string {
	if count == 1 {
		return "1 image"
	}
	return fmt.Sprintf("%d images", count)
}

// imagesNote gives the text that tells an agent with no image support about the images of a prompt. It follows text.
func imagesNote(text string, count int) string {
	note := fmt.Sprintf("[The Owner attached %s. This agent cannot read images.]", imagesText(count))
	if text == "" {
		return note
	}
	return "\n\n" + note
}

type imageInfo struct {
	MIMEType string `json:"mimeType"`
	Size     int    `json:"size"`
}

// promptRow gives the Transcript row of a prompt. The row has no image data, because each Transcript event sends the
// full row to the client.
func promptRow(text string, images []Image) any {
	row := struct {
		Text   string      `json:"text"`
		Images []imageInfo `json:"images,omitempty"`
	}{Text: text}
	for _, image := range images {
		row.Images = append(row.Images, imageInfo{image.MIMEType, len(image.Data)})
	}
	return row
}
