package execution

import (
	"errors"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestCategorizeBatchError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected batchErrorCategory
	}{
		{"nil error", nil, errCategoryNone},
		{"block range", errors.New("block range too large"), errCategoryBlockRange},
		{"exceed", errors.New("query exceed max"), errCategoryBlockRange},
		{"query returned more", errors.New("query returned more than 10000 results"), errCategoryBlockRange},
		{"rate limited", errors.New("rate limited"), errCategoryRateLimit},
		{"too many requests", errors.New("too many requests"), errCategoryRateLimit},
		{"429", errors.New("error 429"), errCategoryRateLimit},
		{"payload too large", errors.New("payload too large"), errCategoryPayloadTooLarge},
		{"413", errors.New("error 413"), errCategoryPayloadTooLarge},
		{"timeout", errors.New("request timeout"), errCategoryTimeout},
		{"context deadline", errors.New("context deadline"), errCategoryTimeout},
		{"generic limit", errors.New("some limit reached"), errCategoryBlockRange},
		{"unrelated error", errors.New("connection refused"), errCategoryNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := categorizeBatchError(tt.err)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestGroupLogsByBlock(t *testing.T) {
	blockTimes := map[uint64]time.Time{
		100: time.Unix(1000, 0).UTC(),
		101: time.Unix(1012, 0).UTC(),
		102: time.Unix(1024, 0).UTC(),
	}

	tests := []struct {
		name        string
		logs        []types.Log
		expected    []BlockLogs
		errContains string
	}{
		{
			name:     "empty logs",
			logs:     nil,
			expected: nil,
		},
		{
			name: "single log",
			logs: []types.Log{
				{BlockNumber: 100, TxIndex: 0, Index: 0},
			},
			expected: []BlockLogs{
				{
					BlockNumber: 100,
					BlockTime:   time.Unix(1000, 0).UTC(),
					Logs:        []types.Log{{BlockNumber: 100, TxIndex: 0, Index: 0}},
				},
			},
		},
		{
			name: "sorts by block, tx, log index",
			logs: []types.Log{
				{BlockNumber: 100, TxIndex: 1, Index: 0},
				{BlockNumber: 100, TxIndex: 0, Index: 1},
				{BlockNumber: 100, TxIndex: 0, Index: 0},
			},
			expected: []BlockLogs{
				{
					BlockNumber: 100,
					BlockTime:   time.Unix(1000, 0).UTC(),
					Logs: []types.Log{
						{BlockNumber: 100, TxIndex: 0, Index: 0},
						{BlockNumber: 100, TxIndex: 0, Index: 1},
						{BlockNumber: 100, TxIndex: 1, Index: 0},
					},
				},
			},
		},
		{
			name: "groups by block",
			logs: []types.Log{
				{BlockNumber: 102, TxIndex: 0, Index: 0},
				{BlockNumber: 100, TxIndex: 0, Index: 0},
				{BlockNumber: 101, TxIndex: 0, Index: 0},
				{BlockNumber: 100, TxIndex: 1, Index: 0},
			},
			expected: []BlockLogs{
				{
					BlockNumber: 100,
					BlockTime:   time.Unix(1000, 0).UTC(),
					Logs: []types.Log{
						{BlockNumber: 100, TxIndex: 0, Index: 0},
						{BlockNumber: 100, TxIndex: 1, Index: 0},
					},
				},
				{
					BlockNumber: 101,
					BlockTime:   time.Unix(1012, 0).UTC(),
					Logs: []types.Log{
						{BlockNumber: 101, TxIndex: 0, Index: 0},
					},
				},
				{
					BlockNumber: 102,
					BlockTime:   time.Unix(1024, 0).UTC(),
					Logs: []types.Log{
						{BlockNumber: 102, TxIndex: 0, Index: 0},
					},
				},
			},
		},
		{
			name: "error on missing block time",
			logs: []types.Log{
				{BlockNumber: 999, TxIndex: 0, Index: 0},
			},
			errContains: "missing block time for block 999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := groupLogsByBlock(tt.logs, blockTimes)

			if tt.errContains != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, result)
			}
		})
	}
}
