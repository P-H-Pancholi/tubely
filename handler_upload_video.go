package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func getVideoAspectRatio(filePath string) (string, error) {
	fmt.Println(filePath)
	cmdRes := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var b, errBuf bytes.Buffer
	cmdRes.Stdout = &b
	cmdRes.Stderr = &errBuf
	if err := cmdRes.Run(); err != nil {
		fmt.Println("ffprobe stderr:", errBuf.String())
		return "", err
	}
	var jsonRes struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	decoder := json.NewDecoder(&b)

	if err := decoder.Decode(&jsonRes); err != nil {
		fmt.Println("37")
		return "", err
	}

	width := jsonRes.Streams[0].Width
	height := jsonRes.Streams[0].Height
	ratio := float64(width) / float64(height)
	const (
		ratio16by9 = 16.0 / 9.0
		ratio9by16 = 9.0 / 16.0
		tolerance  = 0.02
	)

	if math.Abs(ratio-ratio9by16) <= tolerance {
		return "9:16", nil
	}

	if math.Abs(ratio-ratio16by9) <= tolerance {
		return "16:9", nil
	}

	return "other", nil

}

func processVideoForFastStart(filePath string) (string, error) {
	/*
		Takes filePath as input
		Returns new filePath with fast start encoding
	*/

	newfilePath := filePath + ".processing"
	cmdRes := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", newfilePath)
	var b, errBuf bytes.Buffer
	cmdRes.Stdout = &b
	cmdRes.Stderr = &errBuf
	if err := cmdRes.Run(); err != nil {
		fmt.Println("ffprobe stderr:", errBuf.String())
		return "", err
	}
	return newfilePath, nil
}

// func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
// 	client := s3.NewPresignClient(s3Client)

// 	obj, err := client.PresignGetObject(context.Background(), &s3.GetObjectInput{
// 		Bucket: &bucket,
// 		Key:    &key,
// 	}, s3.WithPresignExpires(expireTime))

// 	if err != nil {
// 		return "", err
// 	}

// 	return obj.URL, nil
// }

// func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
// 	if video.VideoURL == nil {
// 		return video, nil
// 	}
// 	urlArray := strings.Split(*video.VideoURL, ",")
// 	bucket := urlArray[0]
// 	key := urlArray[1]

// 	url, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Minute*3)
// 	if err != nil {
// 		return database.Video{}, err
// 	}

// 	video.VideoURL = &url
// 	return video, nil
// }

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const maxValue = 1 << 30
	r.Body = http.MaxBytesReader(w, r.Body, maxValue)
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

	videoData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't get video from db", err)
		return
	}

	if videoData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "user not owner of video", nil)
		return
	}
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to read video from request", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to parse media type", err)
		return
	}

	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusUnsupportedMediaType, "invalid media", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to create temp file", err)
		return
	}
	err = os.Chmod(tempFile.Name(), 0666)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	bytesCopied, err := io.Copy(tempFile, file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error writing file", err)
		return
	}
	fmt.Printf("Copied %d bytes successfully!\n", bytesCopied)

	// err = tempFile.Sync()

	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "Error syncing temp file", err)
	// 	return
	// }

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to process file", err)
		return
	}

	processedFile, err := os.Open(processedFilePath)
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to open process file "+processedFilePath+" ", err)
	}

	_, err = processedFile.Seek(0, io.SeekStart)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error resetting file pointer", err)
		return
	}

	randBytes := make([]byte, 32)
	rand.Read(randBytes)
	randomFileName := base64.RawURLEncoding.EncodeToString(randBytes)

	resolution, err := getVideoAspectRatio(processedFile.Name())

	if err != nil {

		respondWithError(w, http.StatusInternalServerError, "Error reading resolution of video: ", err)
		log.Fatal(err)
		return
	}

	prefix := ""

	if resolution == "9:16" {
		prefix = "portrait/"
	} else if resolution == "16:9" {
		prefix = "landscape/"
	} else {
		prefix = "other/"
	}

	s3Key := fmt.Sprintf("%s%s.%s", prefix, randomFileName, "mp4")
	_, err = cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &s3Key,
		Body:        processedFile,
		ContentType: &mediaType,
	})

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to put object in s3", err)
		return
	}

	videoURL := cfg.s3CfDistribution + s3Key

	videoData.VideoURL = &videoURL

	if err := cfg.db.UpdateVideo(videoData); err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to update db", err)
		return
	}

	// PresignedVideoData, err := cfg.dbVideoToSignedVideo(videoData)

	// if err != nil {
	// 	respondWithError(w, http.StatusInternalServerError, "unable to sign the video", err)
	// 	return
	// }

	fmt.Printf("Successfully uploaded %s file to s3\n", s3Key)

	respondWithJSON(w, http.StatusOK, videoData)

}
