package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type discogsMasterVersion struct {
	ID       int    `json:"id"`
	Label    string `json:"label"`
	Country  string `json:"country"`
	Format   string `json:"format"`
	Released string `json:"released"`
	Thumb    string `json:"thumb"`
}

type discogsMasterVersionsResp struct {
	Versions   []discogsMasterVersion `json:"versions"`
	Pagination struct {
		Pages int `json:"pages"`
	} `json:"pagination"`
}

type discogsReleaseResp struct {
	ID          int      `json:"id"`
	LowestPrice *float64 `json:"lowest_price"`
	Country     string   `json:"country"`
	Notes       string   `json:"notes"`
	Released    string   `json:"released"`
	ResourceURI string   `json:"resource_url"`
	Images      []struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"images"`
}

type mainReleaseBackfill struct {
	ReleaseID      int
	LowestPrice    *float64
	Country        string
	Notes          *string
	Released       string
	ResourceURI    string
	ImageExtension string
	RawCoverData   []byte
	CoverEmbedding []byte
}

func fetchDiscogsMaster(masterID int, httpClient *http.Client) (discogsMasterResp, error) {
	rawURL := fmt.Sprintf("https://api.discogs.com/masters/%d", masterID)
	resp, err := discogsGET(rawURL, httpClient)
	if err != nil {
		return discogsMasterResp{}, fmt.Errorf("master request failed for %d: %w", masterID, err)
	}
	defer resp.Body.Close()

	var out discogsMasterResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return discogsMasterResp{}, fmt.Errorf("decode master response for %d: %w", masterID, err)
	}
	return out, nil
}

func fetchAllVinylVersions(masterID int, httpClient *http.Client) ([]discogsMasterVersion, error) {
	out := []discogsMasterVersion{}
	for page := 1; ; page++ {
		rawURL := fmt.Sprintf("https://api.discogs.com/masters/%d/versions?format=Vinyl&per_page=100&page=%d", masterID, page)
		resp, err := discogsGET(rawURL, httpClient)
		if err != nil {
			return nil, err
		}

		var decoded discogsMasterVersionsResp
		decodeErr := json.NewDecoder(resp.Body).Decode(&decoded)
		resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode versions for master %d page %d: %w", masterID, page, decodeErr)
		}

		out = append(out, decoded.Versions...)
		if decoded.Pagination.Pages <= page || len(decoded.Versions) == 0 {
			break
		}
	}
	return out, nil
}

func buildMainReleasePayload(masterID, releaseID int, httpClient *http.Client) (mainReleaseBackfill, discogsReleaseResp, error) {
	if releaseID <= 0 {
		return mainReleaseBackfill{}, discogsReleaseResp{}, fmt.Errorf("invalid release_id=%d for master %d", releaseID, masterID)
	}

	rawURL := fmt.Sprintf("https://api.discogs.com/releases/%d", releaseID)
	resp, err := discogsGET(rawURL, httpClient)
	if err != nil {
		return mainReleaseBackfill{}, discogsReleaseResp{}, err
	}
	defer resp.Body.Close()

	var release discogsReleaseResp
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return mainReleaseBackfill{}, discogsReleaseResp{}, fmt.Errorf("decode release %d: %w", releaseID, err)
	}

	imageURI := pickDiscogsImageURI(release.Images)
	if imageURI == "" {
		return mainReleaseBackfill{}, release, fmt.Errorf("release %d has no image", releaseID)
	}

	rawImageData, err := downloadDiscogsImage(imageURI, httpClient)
	if err != nil {
		return mainReleaseBackfill{}, release, err
	}
	embedding, err := RequestEmbedding(rawImageData)
	if err != nil {
		return mainReleaseBackfill{}, release, fmt.Errorf("generate embedding for release %d: %w", releaseID, err)
	}

	resourceURI := strings.TrimSpace(release.ResourceURI)
	if resourceURI == "" {
		resourceURI = rawURL
	}

	return mainReleaseBackfill{
		ReleaseID:      releaseID,
		LowestPrice:    release.LowestPrice,
		Country:        release.Country,
		Notes:          stringPtrIfNonEmpty(release.Notes),
		Released:       release.Released,
		ResourceURI:    resourceURI,
		ImageExtension: imageExtensionFromURI(imageURI),
		RawCoverData:   rawImageData,
		CoverEmbedding: EmbeddingToBlob(embedding),
	}, release, nil
}

func versionReleasedYear(v discogsMasterVersion) any {
	released := strings.TrimSpace(v.Released)
	if len(released) < 4 {
		return nil
	}
	year, err := strconv.Atoi(released[:4])
	if err != nil || year <= 0 {
		return nil
	}
	return year
}

func pickDiscogsImageURI(images []struct {
	Type string `json:"type"`
	URI  string `json:"uri"`
}) string {
	for i := range images {
		if strings.EqualFold(images[i].Type, "primary") && strings.TrimSpace(images[i].URI) != "" {
			return images[i].URI
		}
	}
	for i := range images {
		if strings.TrimSpace(images[i].URI) != "" {
			return images[i].URI
		}
	}
	return ""
}

func imageExtensionFromURI(rawURI string) string {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(path.Ext(parsed.Path)), ".")
}

func discogsGET(rawURL string, httpClient *http.Client) (*http.Response, error) {
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

func downloadDiscogsImage(imageURI string, httpClient *http.Client) ([]byte, error) {
	waitForDiscogsRequestSlot()
	resp, err := discogsGET(imageURI, httpClient)
	if err != nil {
		recordDiscogsRequestResult(false)
		return nil, fmt.Errorf("image download failed for %s: %w", imageURI, err)
	}
	defer resp.Body.Close()
	recordDiscogsRequestResult(true)

	rawImageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read image data from %s: %w", imageURI, err)
	}
	if len(rawImageData) == 0 {
		return nil, fmt.Errorf("empty image response from %s", imageURI)
	}
	return rawImageData, nil
}
