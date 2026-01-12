package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
)

type mockStorage struct {
	commit *storage.OracleCommit
	err    error
}

func (m *mockStorage) GetLatestCommit(_ context.Context) (*storage.OracleCommit, error) {
	return m.commit, m.err
}

func TestHandleGetCommit_NoCommit(t *testing.T) {
	server := New(&mockStorage{}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "no commit found", resp.Error)
}

func TestHandleGetCommit_Basic(t *testing.T) {
	commit := &storage.OracleCommit{
		TargetEpoch:    100,
		MerkleRoot:     make([]byte, 32),
		ReferenceBlock: 500000,
		TxHash:         make([]byte, 32),
		ClusterBalances: []storage.ClusterBalance{
			{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		},
	}
	commit.MerkleRoot[0] = 0xff
	commit.TxHash[0] = 0xee
	commit.ClusterBalances[0].ClusterID[0] = 0x11

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint64(100), resp.Epoch)
	require.Equal(t, uint64(500000), resp.ReferenceBlock)
	require.Equal(t, "0xff00000000000000000000000000000000000000000000000000000000000000", resp.MerkleRoot)
	require.Equal(t, "0xee00000000000000000000000000000000000000000000000000000000000000", resp.TxHash)
	require.Nil(t, resp.Clusters)
	require.Nil(t, resp.Layers)
}

func TestHandleGetCommit_FullTree(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	clusterBalances[0].ClusterID[0] = 0x11
	clusterBalances[1].ClusterID[0] = 0x22

	// Compute correct merkle root
	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Clusters, 2)
	require.NotNil(t, resp.Layers)

	// Verify clusters have hashes
	for _, c := range resp.Clusters {
		require.NotEmpty(t, c.ClusterID)
		require.NotEmpty(t, c.Hash)
		require.NotZero(t, c.EffectiveBalance)
	}
}

func TestHandleGetCommit_SingleCluster(t *testing.T) {
	clusterID := [32]byte{0x11}
	effectiveBalance := uint32(32)

	// Compute the expected merkle root (for single cluster, root = leaf hash)
	expectedRoot := merkle.HashLeaf(clusterID, effectiveBalance)

	commit := &storage.OracleCommit{
		TargetEpoch:    100,
		MerkleRoot:     expectedRoot[:],
		ReferenceBlock: 500000,
		TxHash:         make([]byte, 32),
		ClusterBalances: []storage.ClusterBalance{
			{ClusterID: clusterID[:], EffectiveBalance: effectiveBalance},
		},
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Clusters, 1)
	require.Nil(t, resp.Layers) // Single cluster has no inner layers

	// For single cluster, merkle root = cluster hash
	require.Equal(t, resp.Clusters[0].Hash, resp.MerkleRoot)
}

func TestHandleGetProof_InvalidClusterID(t *testing.T) {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"empty", ""},
		{"no prefix", "1100000000000000000000000000000000000000000000000000000000000000"},
		{"too short", "0x11"},
		{"too long", "0x110000000000000000000000000000000000000000000000000000000000000000"},
		{"invalid hex", "0xgg00000000000000000000000000000000000000000000000000000000000000"},
	}

	server := New(&mockStorage{}, "127.0.0.1:0")

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+tt.clusterID, nil)
			req.SetPathValue("clusterId", tt.clusterID)
			rec := httptest.NewRecorder()

			server.handleGetProof(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)

			var resp ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Equal(t, "invalid clusterId format", resp.Error)
		})
	}
}

func TestHandleGetProof_NoCommit(t *testing.T) {
	server := New(&mockStorage{}, "127.0.0.1:0")

	clusterID := "0x1100000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
	req.SetPathValue("clusterId", clusterID)
	rec := httptest.NewRecorder()

	server.handleGetProof(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "no commit found", resp.Error)
}

func TestHandleGetProof_ClusterNotFound(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x11

	// Compute correct merkle root
	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	// Request proof for a different cluster
	clusterID := "0xff00000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
	req.SetPathValue("clusterId", clusterID)
	rec := httptest.NewRecorder()

	server.handleGetProof(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "cluster not found", resp.Error)
}

func TestHandleGetProof_Success(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	clusterBalances[0].ClusterID[0] = 0x11
	clusterBalances[1].ClusterID[0] = 0x22

	// Compute correct merkle root
	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	clusterID := "0x1100000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
	req.SetPathValue("clusterId", clusterID)
	rec := httptest.NewRecorder()

	server.handleGetProof(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp ProofResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, clusterID, resp.ClusterID)
	require.Equal(t, uint32(32), resp.EffectiveBalance)
	require.NotEmpty(t, resp.Proof)
	require.NotEmpty(t, resp.MerkleRoot)
	require.Equal(t, uint64(500000), resp.ReferenceBlock)
}

func TestHandleGetProof_SingleCluster(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x11

	// Compute correct merkle root
	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	clusterID := "0x1100000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
	req.SetPathValue("clusterId", clusterID)
	rec := httptest.NewRecorder()

	server.handleGetProof(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp ProofResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Empty(t, resp.Proof) // Single cluster has empty proof
}

func TestIsValidClusterID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		valid bool
	}{
		{"valid", "0x1100000000000000000000000000000000000000000000000000000000000000", true},
		{"all zeros", "0x0000000000000000000000000000000000000000000000000000000000000000", true},
		{"all ff", "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", true},
		{"empty", "", false},
		{"no prefix", "1100000000000000000000000000000000000000000000000000000000000000", false},
		{"short", "0x11", false},
		{"long", "0x110000000000000000000000000000000000000000000000000000000000000000", false},
		{"invalid hex", "0xgg00000000000000000000000000000000000000000000000000000000000000", false},
		{"uppercase valid", "0xABCDEF0000000000000000000000000000000000000000000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.valid, isValidClusterID(tt.input))
		})
	}
}

func TestToHex(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{"empty", []byte{}, "0x"},
		{"single byte", []byte{0x11}, "0x11"},
		{"multiple bytes", []byte{0xde, 0xad, 0xbe, 0xef}, "0xdeadbeef"},
		{"32 bytes", make([]byte, 32), "0x0000000000000000000000000000000000000000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, toHex(tt.input))
		})
	}
}

func TestContentTypeMiddleware(t *testing.T) {
	server := New(&mockStorage{}, "127.0.0.1:0")

	handler := server.contentTypeMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}

func TestRecoveryMiddleware(t *testing.T) {
	server := New(&mockStorage{}, "127.0.0.1:0")

	handler := server.recoveryMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	// Should not panic
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "internal error", resp.Error)
}

func TestHandleGetCommit_OddLeafCount(t *testing.T) {
	// 3 clusters = odd count, merkle tree duplicates last hash
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
		{ClusterID: make([]byte, 32), EffectiveBalance: 96},
	}
	clusterBalances[0].ClusterID[0] = 0x11
	clusterBalances[1].ClusterID[0] = 0x22
	clusterBalances[2].ClusterID[0] = 0x33

	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Clusters, 3)
	require.NotNil(t, resp.Layers)

	// With 3 leaves: layer 0 has 2 nodes (pairs: [0,1] and [2,2-dup])
	// layer 1 has 1 node (root)
	require.Len(t, resp.Layers, 2)
	require.Len(t, resp.Layers[0], 2) // Two parent nodes
	require.Len(t, resp.Layers[1], 1) // Root
}

func TestHandleGetProof_OddLeafCount(t *testing.T) {
	// 3 clusters = odd count
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
		{ClusterID: make([]byte, 32), EffectiveBalance: 96},
	}
	clusterBalances[0].ClusterID[0] = 0x11
	clusterBalances[1].ClusterID[0] = 0x22
	clusterBalances[2].ClusterID[0] = 0x33

	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	// Get proof for each cluster
	for i, bal := range clusterBalances {
		clusterID := toHex(bal.ClusterID)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
		req.SetPathValue("clusterId", clusterID)
		rec := httptest.NewRecorder()

		server.handleGetProof(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, "cluster %d", i)

		var resp ProofResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, clusterID, resp.ClusterID)
		require.Len(t, resp.Proof, 2, "cluster %d should have 2 proof elements", i)

		// Verify proof against merkle package
		treeProof, err := tree.GetProof([32]byte(bal.ClusterID))
		require.NoError(t, err)
		require.Len(t, treeProof, 2)
	}
}

func TestHandleGetCommit_RootMismatch(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x11

	// Store a wrong merkle root (doesn't match the balances)
	wrongRoot := make([]byte, 32)
	wrongRoot[0] = 0xff

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      wrongRoot,
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	// Request with full=true triggers root verification
	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "internal error", resp.Error)
}

func TestHandleGetProof_RootMismatch(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x11

	// Store a wrong merkle root
	wrongRoot := make([]byte, 32)
	wrongRoot[0] = 0xff

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      wrongRoot,
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	server := New(&mockStorage{commit: commit}, "127.0.0.1:0")

	clusterID := "0x1100000000000000000000000000000000000000000000000000000000000000"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
	req.SetPathValue("clusterId", clusterID)
	rec := httptest.NewRecorder()

	server.handleGetProof(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "internal error", resp.Error)
}

func TestToHexOrEmpty(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected string
	}{
		{"nil", nil, ""},
		{"empty", []byte{}, ""},
		{"single byte", []byte{0x11}, "0x11"},
		{"32 bytes", make([]byte, 32), "0x0000000000000000000000000000000000000000000000000000000000000000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, toHexOrEmpty(tt.input))
		})
	}
}
