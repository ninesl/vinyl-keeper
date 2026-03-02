package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strconv"

	_ "github.com/joho/godotenv/autoload"

	"github.com/ninesl/vinyl-keeper/router"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("missing image path; expected: go run . -path/to/image.jpg")
	}
	imagePath := os.Args[1]
	if imagePath == "" {
		log.Fatalf("empty image path")
	}
	if imagePath[0] == '-' {
		imagePath = imagePath[1:]
	}

	imgData, err := os.ReadFile(imagePath)
	if err != nil {
		log.Fatalf("read image: %v", err)
	}

	params, err := loadImageEmbedParams(imgData)
	if err != nil {
		log.Fatalf("env config: %v", err)
	}

	resp, err := router.SendImageBytes(params)
	if err != nil {
		log.Fatalf("send request: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("read response: %v", err)
	}

	if resp.StatusCode != 200 {
		log.Fatalf("server error %d: %s", resp.StatusCode, string(body))
	}

	// Binary format: 4 bytes uint32 LE (image byte_length) + N*8 bytes float64 LE (embedding)
	if len(body) < 4 {
		log.Fatalf("response too short: %d bytes", len(body))
	}

	byteLength := binary.LittleEndian.Uint32(body[:4])
	embeddingBytes := body[4:]

	if len(embeddingBytes)%8 != 0 {
		log.Fatalf("embedding bytes not aligned to float64: %d bytes", len(embeddingBytes))
	}

	embedding := make([]float64, len(embeddingBytes)/8)
	for i := range embedding {
		bits := binary.LittleEndian.Uint64(embeddingBytes[i*8 : (i+1)*8])
		embedding[i] = math.Float64frombits(bits)
	}

	fmt.Printf("image byte_length: %d\n", byteLength)
	fmt.Printf("embedding dim: %d\n", len(embedding))
	n := min(10, len(embedding))
	fmt.Printf("embedding: %v\n", embedding[:n])
	fmt.Println("...")
}

func loadImageEmbedParams(imgData []byte) (router.ImageEmbedParams, error) {
	host := os.Getenv("IMAGE_SERVICE_HOST")
	if host == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_HOST is not set")
	}

	portRaw := os.Getenv("IMAGE_SERVICE_PORT")
	if portRaw == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_PORT is not set")
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil {
		return router.ImageEmbedParams{}, fmt.Errorf("invalid IMAGE_SERVICE_PORT: %w", err)
	}

	endpoint := os.Getenv("IMAGE_SERVICE_ENDPOINT")
	if endpoint == "" {
		return router.ImageEmbedParams{}, fmt.Errorf("IMAGE_SERVICE_ENDPOINT is not set")
	}

	return router.ImageEmbedParams{
		ImgData:  imgData,
		Host:     host,
		Port:     port,
		Endpoint: endpoint,
	}, nil
}
