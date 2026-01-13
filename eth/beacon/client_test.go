package beacon

import (
	"errors"
	"fmt"
	"testing"

	"github.com/attestantio/go-eth2-client/api"
	"github.com/stretchr/testify/require"
)

func TestIsRetriable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, true},
		{"generic error", errors.New("connection refused"), true},
		{"400 Bad Request", &api.Error{StatusCode: 400}, false},
		{"404 Not Found", &api.Error{StatusCode: 404}, false},
		{"429 Too Many Requests", &api.Error{StatusCode: 429}, true},
		{"500 Internal Server Error", &api.Error{StatusCode: 500}, true},
		{"502 Bad Gateway", &api.Error{StatusCode: 502}, true},
		{"503 Service Unavailable", &api.Error{StatusCode: 503}, true},
		{"wrapped 404", fmt.Errorf("get validators: %w", &api.Error{StatusCode: 404}), false},
		{"wrapped 500", fmt.Errorf("get validators: %w", &api.Error{StatusCode: 500}), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetriable(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}
