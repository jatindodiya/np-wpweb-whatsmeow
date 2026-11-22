package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const (
	maxVideoSize    = 64 << 20
	maxVideoPixels  = 20_000_000
	maxConvertedGIF = 64 << 20
)

var (
	ErrInvalidVideo               = errors.New("invalid video")
	ErrMediaTranscoderUnavailable = errors.New("video transcoder is not installed")
)

type SendVideoMessageResult struct {
	ID            string `json:"messageId"`
	Recipient     string `json:"recipient"`
	Timestamp     string `json:"timestamp"`
	HistoryStored bool   `json:"historyStored"`
	GIF           bool   `json:"gif"`
	ViewOnce      bool   `json:"viewOnce"`
}

type videoMetadata struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (m *Manager) SendVideoMessage(ctx context.Context, id, phoneNumber, caption string, viewOnce bool, data []byte) (SendVideoMessageResult, error) {
	if !ValidSessionID(id) {
		return SendVideoMessageResult{}, errors.New("id must be a UUID")
	}
	id = strings.ToLower(id)
	if len(phoneNumber) < 10 || len(phoneNumber) > 15 || !allPhoneNumberDigits(phoneNumber) {
		return SendVideoMessageResult{}, errors.New("mobileNumber must contain 10 to 15 digits")
	}
	if len(data) == 0 || len(data) > maxVideoSize {
		return SendVideoMessageResult{}, fmt.Errorf("%w: video must be between 1 byte and 64 MiB", ErrInvalidVideo)
	}
	if len([]rune(caption)) > 1024 {
		return SendVideoMessageResult{}, errors.New("caption must be 1024 characters or fewer")
	}

	m.mu.RLock()
	service, exists := m.sessions[id]
	m.mu.RUnlock()
	if !exists || service.rejected.Load() {
		return SendVideoMessageResult{}, ErrSessionNotFound
	}
	client := service.Client
	if !client.IsConnected() || !client.IsLoggedIn() {
		return SendVideoMessageResult{}, ErrSessionNotConnected
	}

	isGIF, err := isGIFFile(data)
	if err != nil {
		return SendVideoMessageResult{}, err
	}
	videoData := data
	if isGIF {
		videoData, err = convertGIFToMP4(ctx, data)
		if err != nil {
			return SendVideoMessageResult{}, err
		}
	}
	if !isMP4(videoData) {
		return SendVideoMessageResult{}, fmt.Errorf("%w: only MP4 videos and GIF images are supported", ErrInvalidVideo)
	}

	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return SendVideoMessageResult{}, fmt.Errorf("%w: ffprobe is required to send video metadata: %v", ErrMediaTranscoderUnavailable, err)
	}
	metadata, err := probeVideo(ctx, ffprobe, videoData)
	if err != nil {
		return SendVideoMessageResult{}, err
	}
	if len(metadata.Streams) == 0 || metadata.Streams[0].Width <= 0 || metadata.Streams[0].Height <= 0 ||
		int64(metadata.Streams[0].Width)*int64(metadata.Streams[0].Height) > maxVideoPixels {
		return SendVideoMessageResult{}, fmt.Errorf("%w: video dimensions are invalid or exceed 20 megapixels", ErrInvalidVideo)
	}
	duration, err := strconv.ParseFloat(metadata.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 {
		return SendVideoMessageResult{}, fmt.Errorf("%w: could not read video duration", ErrInvalidVideo)
	}
	if duration >= float64(math.MaxUint32) {
		return SendVideoMessageResult{}, fmt.Errorf("%w: video duration is too long", ErrInvalidVideo)
	}
	thumbnail, err := makeVideoThumbnail(ctx, videoData)
	if err != nil {
		return SendVideoMessageResult{}, err
	}
	recipient, err := resolveWhatsAppRecipient(ctx, client, phoneNumber)
	if err != nil {
		return SendVideoMessageResult{}, err
	}
	upload, err := client.Upload(ctx, videoData, whatsmeow.MediaVideo)
	if err != nil {
		return SendVideoMessageResult{}, fmt.Errorf("upload WhatsApp video: %w", err)
	}
	seconds := uint32(math.Ceil(duration))
	videoMessage := &waE2E.VideoMessage{
		URL:               proto.String(upload.URL),
		DirectPath:        proto.String(upload.DirectPath),
		MediaKey:          upload.MediaKey,
		FileSHA256:        upload.FileSHA256,
		FileEncSHA256:     upload.FileEncSHA256,
		FileLength:        &upload.FileLength,
		Mimetype:          proto.String("video/mp4"),
		Width:             proto.Uint32(uint32(metadata.Streams[0].Width)),
		Height:            proto.Uint32(uint32(metadata.Streams[0].Height)),
		Seconds:           proto.Uint32(seconds),
		MediaKeyTimestamp: proto.Int64(time.Now().Unix()),
		JPEGThumbnail:     thumbnail,
		StreamingSidecar:  upload.StreamingSidecar,
	}
	if caption != "" {
		videoMessage.Caption = proto.String(caption)
	}
	if isGIF {
		videoMessage.GifPlayback = proto.Bool(true)
	}
	message := &waE2E.Message{VideoMessage: videoMessage}
	if viewOnce {
		videoMessage.ViewOnce = proto.Bool(true)
		message = &waE2E.Message{
			ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: message},
		}
	}
	response, err := client.SendMessage(ctx, recipient, message)
	if err != nil {
		return SendVideoMessageResult{}, fmt.Errorf("send WhatsApp video message: %w", err)
	}

	historyStored := false
	if client.Store != nil && client.Store.ID != nil {
		info := types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:     recipient,
				Sender:   client.Store.ID.ToNonAD(),
				IsFromMe: true,
			},
			ID:        response.ID,
			Type:      "video",
			Timestamp: response.Timestamp,
		}
		if storeErr := service.storeChatMessage(info, message); storeErr != nil {
			fmt.Printf("WhatsApp session %s sent video %s but failed to store chat history: %v\n", id, response.ID, storeErr)
		} else {
			historyStored = true
		}
	}
	return SendVideoMessageResult{
		ID:            response.ID,
		Recipient:     recipient.String(),
		Timestamp:     response.Timestamp.UTC().Format(time.RFC3339Nano),
		HistoryStored: historyStored,
		GIF:           isGIF,
		ViewOnce:      viewOnce,
	}, nil
}

func isGIFFile(data []byte) (bool, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "gif" {
		return false, nil
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxVideoPixels {
		return true, fmt.Errorf("%w: GIF dimensions are invalid or exceed 20 megapixels", ErrInvalidVideo)
	}
	return true, nil
}

func isMP4(data []byte) bool {
	return len(data) >= 12 && string(data[4:8]) == "ftyp"
}

func convertGIFToMP4(ctx context.Context, data []byte) ([]byte, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg is required to convert GIF files to WhatsApp animated videos: %v", ErrMediaTranscoderUnavailable, err)
	}
	input, err := os.CreateTemp("", "whatsapp-gif-*.gif")
	if err != nil {
		return nil, fmt.Errorf("create temporary GIF input: %w", err)
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err := input.Write(data); err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("write temporary GIF input: %w", err)
	}
	if err := input.Close(); err != nil {
		return nil, fmt.Errorf("close temporary GIF input: %w", err)
	}
	output, err := os.CreateTemp("", "whatsapp-gif-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("create temporary MP4 output: %w", err)
	}
	outputPath := output.Name()
	defer os.Remove(outputPath)
	if err := output.Close(); err != nil {
		return nil, fmt.Errorf("close temporary MP4 output: %w", err)
	}

	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
		"-ignore_loop", "1", "-i", inputPath, "-an",
		"-vf", "scale='min(720,iw)':'min(720,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-fs", strconv.Itoa(maxConvertedGIF), "-movflags", "+faststart",
		outputPath,
	)
	var stderr limitedOutput
	stderr.max = 8 << 10
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("convert GIF to MP4: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: convert GIF to MP4: %s", ErrInvalidVideo, strings.TrimSpace(stderr.String()))
	}
	converted, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read converted GIF video: %w", err)
	}
	if len(converted) == 0 || len(converted) > maxConvertedGIF || !isMP4(converted) {
		return nil, fmt.Errorf("%w: converted GIF video is empty, oversized, or invalid", ErrInvalidVideo)
	}
	return converted, nil
}

func probeVideo(ctx context.Context, ffprobe string, data []byte) (videoMetadata, error) {
	input, err := os.CreateTemp("", "whatsapp-video-*.mp4")
	if err != nil {
		return videoMetadata{}, fmt.Errorf("create temporary video input: %w", err)
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err := input.Write(data); err != nil {
		_ = input.Close()
		return videoMetadata{}, fmt.Errorf("write temporary video input: %w", err)
	}
	if err := input.Close(); err != nil {
		return videoMetadata{}, fmt.Errorf("close temporary video input: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "json", inputPath)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return videoMetadata{}, fmt.Errorf("inspect video: %w", ctx.Err())
		}
		return videoMetadata{}, fmt.Errorf("%w: inspect video metadata: %v", ErrInvalidVideo, err)
	}
	var metadata videoMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return videoMetadata{}, fmt.Errorf("%w: parse video metadata: %v", ErrInvalidVideo, err)
	}
	return metadata, nil
}

func makeVideoThumbnail(ctx context.Context, data []byte) ([]byte, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg is required to generate video thumbnails: %v", ErrMediaTranscoderUnavailable, err)
	}
	input, err := os.CreateTemp("", "whatsapp-video-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("create temporary video input: %w", err)
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, err := input.Write(data); err != nil {
		_ = input.Close()
		return nil, fmt.Errorf("write temporary video input: %w", err)
	}
	if err := input.Close(); err != nil {
		return nil, fmt.Errorf("close temporary video input: %w", err)
	}
	cmd := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-nostdin",
		"-ss", "0", "-i", inputPath, "-frames:v", "1",
		"-vf", "scale=w='min(320,iw)':h='min(320,ih)':force_original_aspect_ratio=decrease",
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	var stderr limitedOutput
	stderr.max = 8 << 10
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("generate video thumbnail: %w", ctx.Err())
		}
		return nil, fmt.Errorf("%w: generate video thumbnail: %s", ErrInvalidVideo, strings.TrimSpace(stderr.String()))
	}
	if len(output) == 0 || len(output) > 2<<20 {
		return nil, fmt.Errorf("%w: generated video thumbnail is empty or too large", ErrInvalidVideo)
	}
	return output, nil
}

type limitedOutput struct {
	bytes.Buffer
	max int
}

func (b *limitedOutput) Write(data []byte) (int, error) {
	length := len(data)
	if b.Len() < b.max {
		remaining := b.max - b.Len()
		if length > remaining {
			data = data[:remaining]
		}
		_, _ = b.Buffer.Write(data)
	}
	return length, nil
}

func normalizedUploadName(name string) string {
	name = strings.ReplaceAll(name, `\`, "/")
	return filepath.Base(name)
}
