package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here

	// parse form data
	const maxMemory = 10 << 20 // bit-shifted 10MB
	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Cannot parse multipart form", err)
		return
	}

	// get the image data from the form
	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Cannot parse form file", err)
		return
	}
	defer file.Close()
	// get media type
	contentType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Thumbnail content type is missing", nil)
		return
	}
	if contentType != "image/jpeg" && contentType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", nil)
		return
	}

	// get video's meta data
	videoMeta, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
		return
	}
	// if the user is not the owner respond the unauthorized error
	if videoMeta.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	// update video metadata with thumbnail image stored as data URL
	thumbnailFullName := getAssetFullName(contentType)
	thumbnailPath := cfg.getAssetDiskPath(thumbnailFullName)
	thumbnailFile, err := os.Create(thumbnailPath)
	if err != nil {
		fmt.Println(thumbnailPath)
		respondWithError(w, http.StatusInternalServerError, "Unable to create file on server", err)
		return
	}
	defer thumbnailFile.Close()
	_, err = io.Copy(thumbnailFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error saving file", err)
		return
	}

	// generate thumbnail URL
	thumbnailURL := cfg.getAssetURL(thumbnailFullName)
	videoMeta.ThumbnailURL = &thumbnailURL

	// update video metadata in db
	err = cfg.db.UpdateVideo(videoMeta)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Cannot save media data", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMeta)
}
