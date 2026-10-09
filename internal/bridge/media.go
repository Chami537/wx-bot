package bridge

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/lawrencewzen/wx-bot/internal/ilink"
)

func mediaDest(mediaDir, binding, msgid, filename string, t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	day := t.Format("20060102")
	name := ilink.SanitizeFilename(filename)
	return filepath.Join(mediaDir, binding, day, fmt.Sprintf("%s_%s", msgid, name))
}

func defaultMediaName(itemType int, fileName string) string {
	if fileName != "" {
		return fileName
	}
	switch itemType {
	case ilink.ItemTypeImage:
		return "image.jpg"
	case ilink.ItemTypeVoice:
		return "voice.ogg"
	case ilink.ItemTypeVideo:
		return "video.mp4"
	default:
		return "attachment"
	}
}

func mediaTypeName(itemType int) string {
	switch itemType {
	case ilink.ItemTypeImage:
		return "image"
	case ilink.ItemTypeVoice:
		return "voice"
	case ilink.ItemTypeFile:
		return "file"
	case ilink.ItemTypeVideo:
		return "video"
	default:
		return "file"
	}
}
