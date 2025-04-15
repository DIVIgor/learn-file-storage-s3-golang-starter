package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

// Check video at file path and return its aspect ratio
func getVideoAspectRatio(filePath string) (aspectRatio string, err error) {
	command := exec.Command(
		"ffprobe", "-v", "error",
		"-print_format", "json", "-show_streams",
		filePath,
	)

	var buffer bytes.Buffer
	command.Stdout = &buffer

	err = command.Run()
	if err != nil {
		return aspectRatio, fmt.Errorf("ffprobe error: %v", err)
	}

	var videoDetails struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	err = json.Unmarshal(buffer.Bytes(), &videoDetails)
	if err != nil {
		return aspectRatio, fmt.Errorf("couldn't parse ffprobe output: %v", err)
	}

	if len(videoDetails.Streams) == 0 {
		return aspectRatio, errors.New("no video streams found")
	}

	// aspect ratios we're interested in
	horizontalVid := 16.0 / 9.0
	verticalVid := 9.0 / 16.0
	maxDifference := 0.1
	// check if aspect ratio is within +/- `maxDifference` of specified standards
	aspectRatioFloat := float64(videoDetails.Streams[0].Width) / float64(videoDetails.Streams[0].Height)
	if aspectRatioFloat < horizontalVid+maxDifference && aspectRatioFloat > horizontalVid-maxDifference {
		aspectRatio = "16:9"
	} else if aspectRatioFloat < verticalVid+maxDifference && aspectRatioFloat > verticalVid-maxDifference {
		aspectRatio = "9:16"
	} else {
		aspectRatio = "other"
	}

	return aspectRatio, err
}

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// 1. Set an upload limit of 1 GB (1 << 30 bytes) using http.MaxBytesReader.
	const maxMemory = 1 << 30 // bit-shifted 1GB
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	// 2. Extract the videoID from the URL path parameters and parse it as a UUID
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't parse file ID", err)
		return
	}

	// 3. Authenticate the user to get a userID
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

	// 4. Get the video metadata from the database, if the user is not the video owner,
	// return a http.StatusUnauthorized response
	videoMeta, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
		return
	}
	if videoMeta.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized to update this video", err)
		return
	}

	// 5. Parse the uploaded video file from the form data
	// Use (http.Request).FormFile with the key "video" to get a multipart.File in memory
	videoFile, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Cannot parse form file", err)
		return
	}
	// Remember to defer closing the file with (os.File).Close - we don't want any memory leaks
	defer videoFile.Close()

	// Validate the uploaded file to ensure it's an MP4 video
	// Use mime.ParseMediaType and "video/mp4" as the MIME type
	contentType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid content type", err)
		return
	}
	if contentType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type", nil)
		return
	}

	// Save the uploaded file to a temporary file on disk.
	// Use os.CreateTemp to create a temporary file. I passed in an empty string for the directory
	// to use the system default, and the name "tubely-upload.mp4" (but you can use whatever you want)
	tempFile, err := os.CreateTemp("", "video-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't create temporary file", err)
		return
	}
	// defer remove the temp file with os.Remove
	defer os.Remove(tempFile.Name())
	// defer close the temp file (defer is LIFO, so it will close before the remove)
	defer tempFile.Close()
	// io.Copy the contents over from the wire to the temp file
	_, err = io.Copy(tempFile, videoFile)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't write file to disk", err)
		return
	}

	// Reset the tempFile's file pointer to the beginning with .Seek(0, io.SeekStart) -
	// this will allow us to read the file again from the beginning
	_, err = tempFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't reset file pointer", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read video size", err)
		return
	}

	var directory string
	switch aspectRatio {
	case "16:9":
		directory = "landscape"
	case "9:16":
		directory = "portrait"
	default:
		directory = "other"
	}

	// Put the object into S3 using PutObject. You'll need to provide:
	// The bucket name
	// The file key. Use the same <random-32-byte-hex>.ext format as the key.
	// e.g. 1a2b3c4d5e6f7890abcd1234ef567890.mp4
	// The file contents (body). The temp file is an os.File which implements io.Reader
	// Content type, which is the MIME type of the file.
	fileKey := getAssetFullName(contentType)
	fileKey = filepath.Join(directory, fileKey) // combine directory with file name for S3
	bucketInput := s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileKey,
		Body:        tempFile,
		ContentType: &contentType,
	}
	_, err = cfg.s3Client.PutObject(r.Context(), &bucketInput)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't put object to S3", err)
		return
	}

	// Update the VideoURL of the video record in the database with the S3 bucket and key.
	// S3 URLs are in the format https://<bucket-name>.s3.<region>.amazonaws.com/<key>.
	// Make sure you use the correct region and bucket name!
	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileKey)
	videoMeta.VideoURL = &videoURL
	err = cfg.db.UpdateVideo(videoMeta)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, videoMeta)
}
