package router

import (
	"bytes"
	"fmt"
	"net/http"
)

type ImageEmbedParams struct {
	ImgData  []byte
	Host     string
	Port     int
	Endpoint string
}

func SendImageBytes(params ImageEmbedParams) (*http.Response, error) {
	req, err := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("http://%s:%d%s", params.Host, params.Port, params.Endpoint),
		bytes.NewReader(params.ImgData),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/octet-stream")

	// TODO:FIXME: need HTTP timeout
	client := &http.Client{}
	return client.Do(req)
}
