package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

// get content extension or set as '.bin'
func getContentTypeExt(contentType string) string {
	parts := strings.Split(contentType, "/")
	if len(parts) != 2 {
		return ".bin"
	}
	return "." + parts[1]
}

// get asset name with extension
func getAssetFullName(contentType string) string {
	base := make([]byte, 32)
	_, err := rand.Read(base)
	if err != nil {
		panic("failed to generate random bytes")
	}
	thumbnailID := base64.RawURLEncoding.EncodeToString(base)

	ext := getContentTypeExt(contentType)
	return fmt.Sprintf("%s%s", &thumbnailID, ext)
}

// get asset's path on disk
func (cfg apiConfig) getAssetDiskPath(assetFullName string) string {
	return filepath.Join(cfg.assetsRoot, assetFullName)
}

// get asset's URL based on assets root folder
func (cfg apiConfig) getAssetURL(assetFullName string) string {
	return fmt.Sprintf("http://localhost:8091/assets/%s", assetFullName)
}
