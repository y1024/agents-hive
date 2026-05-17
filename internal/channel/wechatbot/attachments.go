package wechatbot

import (
	"fmt"
	"mime"
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/corespeed-io/wechatbot/golang"

	"github.com/chef-guo/agents-hive/internal/channel"
)

func wechatbotAttachments(msg *SDKMessage) []channel.Attachment {
	if msg == nil {
		return nil
	}
	var out []channel.Attachment
	for idx, image := range msg.Images {
		if image.Media == nil {
			continue
		}
		out = append(out, channel.Attachment{
			Type: "image",
			Key:  wechatbotAttachmentKey("image", idx),
		})
	}
	for idx, file := range msg.Files {
		if file.Media == nil {
			continue
		}
		out = append(out, channel.Attachment{
			Type:     "file",
			Key:      wechatbotAttachmentKey("file", idx),
			FileName: file.FileName,
		})
	}
	for idx, video := range msg.Videos {
		if video.Media == nil {
			continue
		}
		out = append(out, channel.Attachment{
			Type: "video",
			Key:  wechatbotAttachmentKey("video", idx),
		})
	}
	for idx, voice := range msg.Voices {
		if voice.Media == nil {
			continue
		}
		out = append(out, channel.Attachment{
			Type: "voice",
			Key:  wechatbotAttachmentKey("voice", idx),
		})
	}
	return out
}

func wechatbotAttachmentKey(kind string, idx int) string {
	return fmt.Sprintf("%s:%d", kind, idx)
}

func sdkMessageFromAttachment(msg *SDKMessage, att channel.Attachment) (*SDKMessage, error) {
	if msg == nil {
		return nil, fmt.Errorf("wechatbot raw message missing")
	}
	idx, err := wechatbotAttachmentIndex(att.Key)
	if err != nil {
		return nil, err
	}
	sdkMsg := *msg
	switch strings.ToLower(strings.TrimSpace(att.Type)) {
	case "image":
		if idx >= len(msg.Images) || msg.Images[idx].Media == nil {
			return nil, fmt.Errorf("wechatbot image attachment %q not found", att.Key)
		}
		sdkMsg.Type = "image"
		sdkMsg.Images = []sdk.ImageContent{msg.Images[idx]}
		sdkMsg.Files = nil
		sdkMsg.Videos = nil
		sdkMsg.Voices = nil
	case "file":
		if idx >= len(msg.Files) || msg.Files[idx].Media == nil {
			return nil, fmt.Errorf("wechatbot file attachment %q not found", att.Key)
		}
		sdkMsg.Type = "file"
		sdkMsg.Images = nil
		sdkMsg.Files = []sdk.FileContent{msg.Files[idx]}
		sdkMsg.Videos = nil
		sdkMsg.Voices = nil
	case "video":
		if idx >= len(msg.Videos) || msg.Videos[idx].Media == nil {
			return nil, fmt.Errorf("wechatbot video attachment %q not found", att.Key)
		}
		sdkMsg.Type = "video"
		sdkMsg.Images = nil
		sdkMsg.Files = nil
		sdkMsg.Videos = []sdk.VideoContent{msg.Videos[idx]}
		sdkMsg.Voices = nil
	case "voice", "audio":
		if idx >= len(msg.Voices) || msg.Voices[idx].Media == nil {
			return nil, fmt.Errorf("wechatbot voice attachment %q not found", att.Key)
		}
		sdkMsg.Type = "voice"
		sdkMsg.Images = nil
		sdkMsg.Files = nil
		sdkMsg.Videos = nil
		sdkMsg.Voices = []sdk.VoiceContent{msg.Voices[idx]}
	default:
		return nil, fmt.Errorf("unsupported wechatbot attachment type %q", att.Type)
	}
	return &sdkMsg, nil
}

func wechatbotAttachmentIndex(key string) (int, error) {
	_, raw, ok := strings.Cut(strings.TrimSpace(key), ":")
	if !ok {
		raw = strings.TrimSpace(key)
	}
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 0 {
		return 0, fmt.Errorf("invalid wechatbot attachment key %q", key)
	}
	return idx, nil
}

func wechatbotAttachmentMimeType(kind, format, filename string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "image":
		return "image/png"
	case "video":
		return "video/mp4"
	case "voice", "audio":
		switch strings.ToLower(strings.TrimSpace(format)) {
		case "silk":
			return "audio/silk"
		default:
			return "audio/mpeg"
		}
	case "file":
		if mt := mime.TypeByExtension(filepath.Ext(filename)); mt != "" {
			return mt
		}
	}
	return "application/octet-stream"
}
