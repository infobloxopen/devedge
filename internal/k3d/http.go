package k3d

import (
	"context"
	"io"
	"net/http"
)

func newHTTPRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, method, url, body)
}

func httpDo(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}
