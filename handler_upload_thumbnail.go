package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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

	const maxValue = 10 << 20
	r.ParseMultipartForm(maxValue)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading thumbnail file", err)
		return
	}
	defer file.Close()
	imageData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading image data", err)
		return
	}
	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading metadata", err)
		return
	}
	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User not owner of video", nil)
		return
	}
	thumbnailData := thumbnail{
		data:      imageData,
		mediaType: header.Header.Get("Content-Type"),
	}
	// imageEnc := base64.StdEncoding.EncodeToString(thumbnailData.data)
	// thumbnailUrl := fmt.Sprintf("data:%s;base64,%s", thumbnailData.mediaType, imageEnc)
	extention := strings.Split(thumbnailData.mediaType, "/")[1]
	filePath := filepath.Join(cfg.assetsRoot, fmt.Sprintf("%s.%s", videoID, extention))
	imageFile, err := os.Create(filePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error creating file", err)
		return
	}
	defer imageFile.Close()
	// Reset file pointer before copying
	if seeker, ok := file.(io.Seeker); ok {
		_, err := seeker.Seek(0, io.SeekStart)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "Error resetting file pointer", err)
			return
		}
	}
	bytesCopied, err := io.Copy(imageFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing file", err)
		return
	}
	fmt.Printf("Copied %d bytes successfully!\n", bytesCopied)

	thumbnailUrl := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, videoID, extention)
	videoData.ThumbnailURL = &thumbnailUrl
	cfg.db.UpdateVideo(videoData)

	// TODO: implement the upload here

	respondWithJSON(w, http.StatusOK, videoData)

}
