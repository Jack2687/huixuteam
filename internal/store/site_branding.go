package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"strings"
)

//go:embed assets/huixu-icon.svg
var DefaultBrandIcon []byte

const MaxBrandIconBytes = 1024 * 1024

type SiteBranding struct {
	IconURL    string `json:"icon_url"`
	Customized bool   `json:"customized"`
}

func brandMetadata(revision string) SiteBranding {
	v := SiteBranding{Customized: revision != ""}
	if revision == "" {
		revision = fmt.Sprintf("%x", sha256.Sum256(DefaultBrandIcon))
	}
	v.IconURL = "/api/public/brand-icon?v=" + revision
	return v
}

func (s *Store) SiteBranding(ctx context.Context) (SiteBranding, error) {
	var revision string
	err := s.Pool.QueryRow(ctx, "SELECT revision FROM site_branding WHERE id").Scan(&revision)
	return brandMetadata(revision), err
}

func (s *Store) BrandIcon(ctx context.Context) ([]byte, string, error) {
	var data []byte
	var mime string
	err := s.Pool.QueryRow(ctx, "SELECT icon_data,icon_mime FROM site_branding WHERE id").Scan(&data, &mime)
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return DefaultBrandIcon, "image/svg+xml", nil
	}
	return data, mime, nil
}

func normalizeBrandIcon(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 || len(raw) > MaxBrandIconBytes {
		return nil, "", invalid("图标文件不能为空且不能超过1MB")
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "<") {
		clean, err := validateMenuSVG(trimmed)
		if err != nil {
			return nil, "", err
		}
		return []byte(clean), "image/svg+xml", nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (format != "png" && format != "jpeg") || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 2048 || cfg.Height > 2048 {
		return nil, "", invalid("请上传PNG、JPG或静态SVG图标，图片宽高不能超过2048像素")
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", invalid("图片文件损坏，无法读取")
	}
	// Re-encode raster images so metadata, appended content and the client's
	// declared MIME type are never served back as uploaded.
	var out bytes.Buffer
	if err = png.Encode(&out, img); err != nil {
		return nil, "", err
	}
	if out.Len() > MaxBrandIconBytes {
		return nil, "", invalid("图片处理后超过1MB，请缩小尺寸后上传")
	}
	return out.Bytes(), "image/png", nil
}

func (s *Store) SaveBrandIcon(ctx context.Context, actor string, raw []byte, reset bool) (SiteBranding, error) {
	data, mime, revision := []byte{}, "", ""
	var err error
	if !reset {
		data, mime, err = normalizeBrandIcon(raw)
		if err != nil {
			return SiteBranding{}, err
		}
		revision = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return SiteBranding{}, err
	}
	defer tx.Rollback(ctx)
	if err = adminLock(ctx, tx, actor); err != nil {
		return SiteBranding{}, err
	}
	if _, err = tx.Exec(ctx, "UPDATE site_branding SET icon_data=$1,icon_mime=$2,revision=$3,updated_at=now() WHERE id", data, mime, revision); err != nil {
		return SiteBranding{}, err
	}
	if err = audit(ctx, tx, actor, "site.branding", "site", map[string]any{"reset": reset, "format": mime, "bytes": len(data)}); err != nil {
		return SiteBranding{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return SiteBranding{}, err
	}
	return brandMetadata(revision), nil
}
