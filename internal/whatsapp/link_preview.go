package whatsapp

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	maxPreviewHTMLBytes  = 10 << 20
	maxPreviewImageBytes = 8 << 20
	maxPreviewPixels     = 8_000_000
	maxThumbnailSize     = 320
)

type linkPreview struct {
	URL         string
	Title       string
	Description string
	Image       []byte
	ImageError  string
}

func buildLinkPreview(ctx context.Context, rawURL string) (linkPreview, error) {
	parsed, err := validatePreviewURL(ctx, rawURL)
	if err != nil {
		return linkPreview{}, err
	}

	client := previewHTTPClient()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return linkPreview{}, fmt.Errorf("create preview request: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NotifyProLinkPreview/1.0)")
	request.Header.Set("Accept", "text/html,application/xhtml+xml")

	response, err := client.Do(request)
	if err != nil {
		return linkPreview{}, fmt.Errorf("fetch link preview page: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return linkPreview{}, fmt.Errorf("link preview page returned HTTP %d", response.StatusCode)
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return linkPreview{}, fmt.Errorf("link preview page is not HTML")
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxPreviewHTMLBytes+1))
	if err != nil {
		return linkPreview{}, fmt.Errorf("read link preview page: %w", err)
	}
	if len(body) > maxPreviewHTMLBytes {
		body = body[:maxPreviewHTMLBytes]
	}
	metadata, err := parsePreviewMetadata(bytes.NewReader(body))
	if err != nil {
		return linkPreview{}, err
	}
	baseURL := parsed
	metadata.URL = parsed.String()
	if response.Request != nil && response.Request.URL != nil {
		baseURL = response.Request.URL
		metadata.URL = baseURL.String()
	}
	if metadata.ImageURL != nil {
		metadata.ImageURL = baseURL.ResolveReference(metadata.ImageURL)
		imageBytes, imageErr := fetchPreviewImage(ctx, client, metadata.ImageURL.String())
		if imageErr != nil {
			metadata.ImageError = imageErr.Error()
		} else {
			metadata.Image = imageBytes
		}
	}
	return linkPreview{
		URL:         metadata.URL,
		Title:       metadata.Title,
		Description: metadata.Description,
		Image:       metadata.Image,
		ImageError:  metadata.ImageError,
	}, nil
}

type previewMetadata struct {
	URL         string
	Title       string
	Description string
	ImageURL    *url.URL
	Image       []byte
	ImageError  string
}

func parsePreviewMetadata(reader io.Reader) (previewMetadata, error) {
	var result previewMetadata
	tokenizer := html.NewTokenizer(reader)
	tokenizer.SetMaxBuf(maxPreviewHTMLBytes)
	var pageTitle strings.Builder
	inTitle := false
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			if tokenizer.Err() == io.EOF {
				break
			}
			if result.Title != "" {
				break
			}
			return previewMetadata{}, fmt.Errorf("parse link preview HTML: %w", tokenizer.Err())
		}
		token := tokenizer.Token()
		switch tokenType {
		case html.StartTagToken, html.SelfClosingTagToken:
			if token.Data == "title" {
				inTitle = true
			}
			if token.Data != "meta" {
				continue
			}
			var name, content string
			for _, attr := range token.Attr {
				switch strings.ToLower(attr.Key) {
				case "property", "name":
					name = strings.ToLower(strings.TrimSpace(attr.Val))
				case "content":
					content = strings.TrimSpace(attr.Val)
				}
			}
			switch name {
			case "og:title", "twitter:title":
				if result.Title == "" {
					result.Title = content
				}
			case "og:description", "description", "twitter:description":
				if result.Description == "" {
					result.Description = content
				}
			case "og:image", "og:image:url", "twitter:image":
				if result.ImageURL == nil && content != "" {
					if parsed, err := url.Parse(content); err == nil {
						result.ImageURL = parsed
					}
				}
			}
		case html.EndTagToken:
			if token.Data == "title" {
				inTitle = false
			}
		case html.TextToken:
			if inTitle && result.Title == "" {
				pageTitle.WriteString(token.Data)
			}
		}
	}
	if result.Title == "" {
		result.Title = strings.TrimSpace(pageTitle.String())
	}
	if result.Title == "" {
		return previewMetadata{}, fmt.Errorf("link preview page has no title")
	}
	result.Title = limitPreviewText(result.Title, 256)
	result.Description = limitPreviewText(result.Description, 1024)
	return result, nil
}

func fetchPreviewImage(ctx context.Context, client *http.Client, imageURL string) ([]byte, error) {
	parsed, err := validatePreviewURL(ctx, imageURL)
	if err != nil {
		return nil, fmt.Errorf("invalid link preview image URL: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create link preview image request: %w", err)
	}
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; NotifyProLinkPreview/1.0)")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch link preview image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("link preview image returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxPreviewImageBytes {
		return nil, fmt.Errorf("link preview image exceeds size limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxPreviewImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read link preview image: %w", err)
	}
	if len(data) > maxPreviewImageBytes {
		return nil, fmt.Errorf("link preview image exceeds size limit")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode link preview image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxPreviewPixels {
		return nil, fmt.Errorf("link preview image dimensions exceed limit")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode link preview image: %w", err)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	scale := float64(maxThumbnailSize) / float64(max(width, height))
	thumbWidth := max(1, int(float64(width)*scale))
	thumbHeight := max(1, int(float64(height)*scale))
	thumbnail := image.NewRGBA(image.Rect(0, 0, thumbWidth, thumbHeight))
	for y := 0; y < thumbHeight; y++ {
		for x := 0; x < thumbWidth; x++ {
			sourceX := bounds.Min.X + x*width/thumbWidth
			sourceY := bounds.Min.Y + y*height/thumbHeight
			thumbnail.Set(x, y, img.At(sourceX, sourceY))
		}
	}
	var output bytes.Buffer
	if err := jpeg.Encode(&output, thumbnail, &jpeg.Options{Quality: 75}); err != nil {
		return nil, fmt.Errorf("encode link preview thumbnail: %w", err)
	}
	return output.Bytes(), nil
}

func limitPreviewText(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > maxRunes {
		return string(runes[:maxRunes])
	}
	return string(runes)
}

func validatePreviewURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse link preview URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("link preview URL must be a public HTTP(S) URL")
	}
	if err := validatePublicHost(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validatePublicHost(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("link preview URL resolves to a non-public address")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve link preview host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("link preview host has no IP addresses")
	}
	for _, entry := range ips {
		if !isPublicIP(entry.IP) {
			return fmt.Errorf("link preview URL resolves to a non-public address")
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		for _, block := range []string{
			"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
			"192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24",
			"203.0.113.0/24", "240.0.0.0/4",
		} {
			_, network, _ := net.ParseCIDR(block)
			if network.Contains(ip4) {
				return false
			}
		}
		return true
	}
	return ipInNetwork(ip, "2000::/3") &&
		!ipInNetwork(ip, "2001::/23") &&
		!ipInNetwork(ip, "2002::/16")
}

func ipInNetwork(ip net.IP, block string) bool {
	_, network, _ := net.ParseCIDR(block)
	return network.Contains(ip)
}

func previewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var dialErrors []string
			for _, entry := range ips {
				if !isPublicIP(entry.IP) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(entry.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				dialErrors = append(dialErrors, err.Error())
			}
			if len(dialErrors) > 0 {
				return nil, fmt.Errorf("could not connect to public preview host: %s", strings.Join(dialErrors, "; "))
			}
			return nil, fmt.Errorf("link preview host has no public IP addresses")
		},
	}
	return &http.Client{
		Transport: transport,
		Timeout:   8 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many link preview redirects")
			}
			_, err := validatePreviewURL(request.Context(), request.URL.String())
			return err
		},
	}
}
