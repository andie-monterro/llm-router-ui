package keys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/michaelquigley/df/dl"
)

const maxHTTPKeyBodyBytes int64 = 32 << 20

type httpSource struct {
	name         string
	endpoint     string
	token        string
	client       *http.Client
	timeout      time.Duration
	etag         string
	maxBodyBytes int64
}

func newHTTPSource(cfg *HTTPSourceConfig, client *http.Client) (*httpSource, error) {
	if client == nil {
		return nil, fmt.Errorf("source '%s': HTTP client is required", cfg.Name)
	}
	endpoint, err := url.JoinPath(cfg.BaseURL, "v1/keys")
	if err != nil {
		return nil, fmt.Errorf("source '%s': build key API endpoint: %w", cfg.Name, err)
	}
	return &httpSource{
		name:         cfg.Name,
		endpoint:     endpoint,
		token:        cfg.Token,
		client:       client,
		timeout:      cfg.Timeout,
		maxBodyBytes: maxHTTPKeyBodyBytes,
	}, nil
}

func (s *httpSource) Name() string {
	return s.name
}

func (s *httpSource) Load(ctx context.Context) (LoadResult, error) {
	requestCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, s.endpoint, nil)
	if err != nil {
		return LoadResult{}, fmt.Errorf("build key API request: %w", err)
	}
	request.Header.Set("Cache-Control", "no-cache")
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	requestedETag := s.etag
	if requestedETag != "" {
		request.Header.Set("If-None-Match", requestedETag)
	}

	response, err := s.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return LoadResult{}, sanitizeKeyAPIRequestError(err)
	}
	defer response.Body.Close()

	if hasPagination(response.Header) {
		return LoadResult{}, fmt.Errorf("key API response uses pagination, which schema version 1 does not support")
	}

	switch response.StatusCode {
	case http.StatusNotModified:
		if requestedETag == "" {
			return LoadResult{}, fmt.Errorf("key API returned HTTP 304 to an unconditional request")
		}
		return Unchanged(), nil
	case http.StatusUnauthorized, http.StatusForbidden:
		dl.Errorf("key source '%s' key API rejected its credential with HTTP %d", s.name, response.StatusCode)
		return LoadResult{}, fmt.Errorf("key API rejected its credential with HTTP %d", response.StatusCode)
	case http.StatusOK:
		// continue below.
	default:
		return LoadResult{}, fmt.Errorf("key API returned HTTP %d", response.StatusCode)
	}

	body, err := readCappedBody(response.Body, s.maxBodyBytes)
	if err != nil {
		return LoadResult{}, err
	}
	contribution, err := decodeJSONContribution(body)
	if err != nil {
		return LoadResult{}, err
	}

	// assignment is deliberate: a valid 200 without an ETag clears the
	// validator held for the previous representation.
	s.etag = response.Header.Get("ETag")
	return Updated(contribution), nil
}

func sanitizeKeyAPIRequestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("key API request timed out: %w", context.DeadlineExceeded)
	}
	var urlError *url.Error
	if errors.As(err, &urlError) {
		err = urlError.Err
	}
	return fmt.Errorf("key API request failed: %w", err)
}

func readCappedBody(body io.Reader, maximum int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read key API response body: %w", err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("key API response body exceeds %d bytes", maximum)
	}
	return data, nil
}

func hasPagination(header http.Header) bool {
	if len(header.Values("Content-Range")) > 0 {
		return true
	}
	for _, value := range header.Values("Link") {
		for _, link := range splitHeaderValue(value, ',') {
			endTarget := strings.IndexByte(link, '>')
			if endTarget < 0 {
				continue
			}
			for _, parameter := range splitHeaderValue(link[endTarget+1:], ';') {
				parts := strings.SplitN(parameter, "=", 2)
				if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), "rel") {
					continue
				}
				relations := strings.Fields(strings.Trim(strings.TrimSpace(parts[1]), `"`))
				for _, relation := range relations {
					switch strings.ToLower(relation) {
					case "next", "prev", "first", "last":
						return true
					}
				}
			}
		}
	}
	return false
}

func splitHeaderValue(value string, separator byte) []string {
	var result []string
	start := 0
	inTarget := false
	inQuote := false
	escaped := false
	for i := 0; i < len(value); i++ {
		character := value[i]
		if escaped {
			escaped = false
			continue
		}
		if inQuote && character == '\\' {
			escaped = true
			continue
		}
		switch character {
		case '<':
			if !inQuote {
				inTarget = true
			}
		case '>':
			if !inQuote {
				inTarget = false
			}
		case '"':
			if !inTarget {
				inQuote = !inQuote
			}
		default:
			if character == separator && !inTarget && !inQuote {
				result = append(result, value[start:i])
				start = i + 1
			}
		}
	}
	return append(result, value[start:])
}
