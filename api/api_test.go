package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ssvlabs/ssv-oracle/merkle"
	"github.com/ssvlabs/ssv-oracle/storage"
)

type mockStorage struct {
	commit      *storage.OracleCommit
	commits     map[uint64]*storage.OracleCommit // epoch -> commit
	clusterInfo storage.AllClusterInfo
	err         error
}

func (m *mockStorage) GetLatestCommit(_ context.Context) (*storage.OracleCommit, error) {
	return m.commit, m.err
}

func (m *mockStorage) GetCommitByEpoch(_ context.Context, epoch uint64) (*storage.OracleCommit, *uint64, *uint64, error) {
	if m.err != nil {
		return nil, nil, nil, m.err
	}
	c, ok := m.commits[epoch]
	if !ok {
		return nil, nil, nil, nil
	}
	var prev, next *uint64
	for e := range m.commits {
		if e < epoch && (prev == nil || e > *prev) {
			v := e
			prev = &v
		}
		if e > epoch && (next == nil || e < *next) {
			v := e
			next = &v
		}
	}
	return c, prev, next, nil
}

func (m *mockStorage) GetAllClusterInfo(_ context.Context) (storage.AllClusterInfo, error) {
	if m.clusterInfo != nil {
		return m.clusterInfo, nil
	}
	return make(storage.AllClusterInfo), nil
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

func TestHandleGetCommit_StorageError(t *testing.T) {
	server := New(&mockStorage{err: fmt.Errorf("db connection lost")}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "internal error", resp.Error)
}

func TestHandleGetCommit_InvalidEpoch(t *testing.T) {
	server := New(&mockStorage{}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=abc", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "invalid epoch parameter", resp.Error)
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

	server := New(&mockStorage{
		commit:  commit,
		commits: map[uint64]*storage.OracleCommit{100: commit},
	}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, commit.TargetEpoch, resp.Epoch)
	require.Equal(t, commit.ReferenceBlock, resp.ReferenceBlock)
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

	server := New(&mockStorage{
		commit:  commit,
		commits: map[uint64]*storage.OracleCommit{100: commit},
	}, "127.0.0.1:0")

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

func TestHandleGetCommit_ClusterInfoEnrichment(t *testing.T) {
	clusterBalances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	clusterBalances[0].ClusterID[0] = 0x11

	tree := buildTree(clusterBalances)

	commit := &storage.OracleCommit{
		TargetEpoch:     100,
		MerkleRoot:      tree.Root[:],
		ReferenceBlock:  500000,
		TxHash:          make([]byte, 32),
		ClusterBalances: clusterBalances,
	}

	ownerAddr := []byte{0xaa, 0xbb}
	clusterInfo := storage.AllClusterInfo{
		fmt.Sprintf("%x", clusterBalances[0].ClusterID): {
			OwnerAddress: ownerAddr,
			OperatorIDs:  []uint64{1, 2, 3, 4},
		},
	}

	server := New(&mockStorage{
		commit:      commit,
		commits:     map[uint64]*storage.OracleCommit{100: commit},
		clusterInfo: clusterInfo,
	}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Clusters, 1)
	require.Equal(t, toHex(ownerAddr), resp.Clusters[0].OwnerAddress)
	require.Equal(t, []uint64{1, 2, 3, 4}, resp.Clusters[0].OperatorIDs)
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

	server := New(&mockStorage{
		commit:  commit,
		commits: map[uint64]*storage.OracleCommit{100: commit},
	}, "127.0.0.1:0")

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

func TestHandleGetProof_WithEpoch(t *testing.T) {
	balances100 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	balances100[0].ClusterID[0] = 0x11
	balances100[1].ClusterID[0] = 0x22
	tree100 := buildTree(balances100)

	balances200 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 40},
	}
	balances200[0].ClusterID[0] = 0x11
	tree200 := buildTree(balances200)

	commits := map[uint64]*storage.OracleCommit{
		100: {
			TargetEpoch: 100, MerkleRoot: tree100.Root[:], ReferenceBlock: 500000,
			TxHash: make([]byte, 32), ClusterBalances: balances100,
		},
		200: {
			TargetEpoch: 200, MerkleRoot: tree200.Root[:], ReferenceBlock: 600000,
			TxHash: make([]byte, 32), ClusterBalances: balances200,
		},
	}

	server := New(&mockStorage{commit: commits[200], commits: commits}, "127.0.0.1:0")

	clusterID := "0x1100000000000000000000000000000000000000000000000000000000000000"

	t.Run("epoch 100 returns proof from historical commit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID+"?epoch=100", nil)
		req.SetPathValue("clusterId", clusterID)
		rec := httptest.NewRecorder()

		server.handleGetProof(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var resp ProofResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, uint32(32), resp.EffectiveBalance)
		require.NotEmpty(t, resp.Proof) // 2 clusters = 1 proof element
		require.Equal(t, toHex(tree100.Root[:]), resp.MerkleRoot)
	})

	t.Run("no epoch returns latest", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/proof/"+clusterID, nil)
		req.SetPathValue("clusterId", clusterID)
		rec := httptest.NewRecorder()

		server.handleGetProof(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var resp ProofResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, uint32(40), resp.EffectiveBalance)
		require.Equal(t, toHex(tree200.Root[:]), resp.MerkleRoot)
	})
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

func TestParseClusterID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "0x1100000000000000000000000000000000000000000000000000000000000000", false},
		{"all zeros", "0x0000000000000000000000000000000000000000000000000000000000000000", false},
		{"all ff", "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", false},
		{"empty", "", true},
		{"no prefix", "1100000000000000000000000000000000000000000000000000000000000000", true},
		{"short", "0x11", true},
		{"long", "0x110000000000000000000000000000000000000000000000000000000000000000", true},
		{"invalid hex", "0xgg00000000000000000000000000000000000000000000000000000000000000", true},
		{"uppercase valid", "0xABCDEF0000000000000000000000000000000000000000000000000000000000", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseClusterID(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
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

	server := New(&mockStorage{
		commit:  commit,
		commits: map[uint64]*storage.OracleCommit{100: commit},
	}, "127.0.0.1:0")

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

	server := New(&mockStorage{
		commit:  commit,
		commits: map[uint64]*storage.OracleCommit{100: commit},
	}, "127.0.0.1:0")

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

func TestHandleGetCommit_EpochParam(t *testing.T) {
	balances1 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	balances1[0].ClusterID[0] = 0x11
	tree1 := buildTree(balances1)

	balances2 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	balances2[0].ClusterID[0] = 0x11
	tree2 := buildTree(balances2)

	commits := map[uint64]*storage.OracleCommit{
		100: {
			TargetEpoch:     100,
			MerkleRoot:      tree1.Root[:],
			ReferenceBlock:  500000,
			TxHash:          make([]byte, 32),
			ClusterBalances: balances1,
			Status:          storage.CommitStatusConfirmed,
		},
		200: {
			TargetEpoch:     200,
			MerkleRoot:      tree2.Root[:],
			ReferenceBlock:  600000,
			TxHash:          make([]byte, 32),
			ClusterBalances: balances2,
			Status:          storage.CommitStatusConfirmed,
		},
	}

	server := New(&mockStorage{commit: commits[200], commits: commits}, "127.0.0.1:0")

	t.Run("specific epoch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=100", nil)
		rec := httptest.NewRecorder()

		server.handleGetCommit(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var resp CommitResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, uint64(100), resp.Epoch)
		require.Nil(t, resp.PreviousEpoch)
		require.NotNil(t, resp.NextEpoch)
		require.Equal(t, uint64(200), *resp.NextEpoch)
	})

	t.Run("latest has prev", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=200", nil)
		rec := httptest.NewRecorder()

		server.handleGetCommit(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)

		var resp CommitResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Equal(t, uint64(200), resp.Epoch)
		require.NotNil(t, resp.PreviousEpoch)
		require.Equal(t, uint64(100), *resp.PreviousEpoch)
		require.Nil(t, resp.NextEpoch)
	})

	t.Run("nonexistent epoch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=999", nil)
		rec := httptest.NewRecorder()

		server.handleGetCommit(rec, req)

		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("invalid epoch", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=abc", nil)
		rec := httptest.NewRecorder()

		server.handleGetCommit(rec, req)

		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHandleGetCommit_TotalBalanceAndDiff(t *testing.T) {
	balances1 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
	}
	balances1[0].ClusterID[0] = 0x11
	balances1[1].ClusterID[0] = 0x22
	tree1 := buildTree(balances1)

	balances2 := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 40},
		{ClusterID: make([]byte, 32), EffectiveBalance: 64},
		{ClusterID: make([]byte, 32), EffectiveBalance: 128},
	}
	balances2[0].ClusterID[0] = 0x11
	balances2[1].ClusterID[0] = 0x22
	balances2[2].ClusterID[0] = 0x33
	tree2 := buildTree(balances2)

	commits := map[uint64]*storage.OracleCommit{
		100: {
			TargetEpoch:     100,
			MerkleRoot:      tree1.Root[:],
			ReferenceBlock:  500000,
			TxHash:          make([]byte, 32),
			ClusterBalances: balances1,
			Status:          storage.CommitStatusConfirmed,
		},
		200: {
			TargetEpoch:     200,
			MerkleRoot:      tree2.Root[:],
			ReferenceBlock:  600000,
			TxHash:          make([]byte, 32),
			ClusterBalances: balances2,
			Status:          storage.CommitStatusConfirmed,
		},
	}

	server := New(&mockStorage{commit: commits[200], commits: commits}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=200&full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	// Total balance: 40 + 64 + 128 = 232
	require.Equal(t, uint64(232), resp.TotalEffectiveBalance)

	require.NotNil(t, resp.BalanceDiff)
	require.Equal(t, uint64(100), resp.BalanceDiff.PreviousEpoch)
	require.Len(t, resp.BalanceDiff.Changed, 1)
	require.Equal(t, uint32(32), resp.BalanceDiff.Changed[0].OldBalance)
	require.Equal(t, uint32(40), resp.BalanceDiff.Changed[0].NewBalance)
	require.Len(t, resp.BalanceDiff.Added, 1)
	require.Equal(t, uint32(128), resp.BalanceDiff.Added[0].Balance)
}

func TestHandleGetCommit_FirstCommitNoDiff(t *testing.T) {
	balances := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	balances[0].ClusterID[0] = 0x11
	tree := buildTree(balances)

	commits := map[uint64]*storage.OracleCommit{
		100: {
			TargetEpoch:     100,
			MerkleRoot:      tree.Root[:],
			ReferenceBlock:  500000,
			TxHash:          make([]byte, 32),
			ClusterBalances: balances,
			Status:          storage.CommitStatusConfirmed,
		},
	}

	server := New(&mockStorage{commit: commits[100], commits: commits}, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/commit?epoch=100&full=true", nil)
	rec := httptest.NewRecorder()

	server.handleGetCommit(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp CommitResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, uint64(32), resp.TotalEffectiveBalance)
	require.Nil(t, resp.BalanceDiff)
	require.Nil(t, resp.PreviousEpoch)
	require.Nil(t, resp.NextEpoch)
}

func testClusterID(prefix byte) []byte {
	id := make([]byte, 32)
	id[0] = prefix
	return id
}

func TestComputeDiff(t *testing.T) {
	old := []storage.ClusterBalance{
		{ClusterID: testClusterID(0x11), EffectiveBalance: 32},
		{ClusterID: testClusterID(0x22), EffectiveBalance: 64},
		{ClusterID: testClusterID(0x33), EffectiveBalance: 96},
	}
	cur := []storage.ClusterBalance{
		{ClusterID: testClusterID(0x11), EffectiveBalance: 40},
		{ClusterID: testClusterID(0x22), EffectiveBalance: 64},
		{ClusterID: testClusterID(0x44), EffectiveBalance: 128},
	}

	diff := computeDiff(50, old, cur)

	require.NotNil(t, diff)
	require.Equal(t, uint64(50), diff.PreviousEpoch)
	require.Len(t, diff.Changed, 1)
	require.Equal(t, uint32(32), diff.Changed[0].OldBalance)
	require.Equal(t, uint32(40), diff.Changed[0].NewBalance)
	require.Len(t, diff.Added, 1)
	require.Equal(t, uint32(128), diff.Added[0].Balance)
	require.Len(t, diff.Removed, 1)
	require.Equal(t, uint32(96), diff.Removed[0].Balance)
}

func TestComputeDiff_NoChanges(t *testing.T) {
	bal := []storage.ClusterBalance{
		{ClusterID: make([]byte, 32), EffectiveBalance: 32},
	}
	diff := computeDiff(50, bal, bal)
	require.Nil(t, diff)
}

func TestComputeDiff_OnlyAddedRemoved(t *testing.T) {
	old := []storage.ClusterBalance{
		{ClusterID: testClusterID(0x11), EffectiveBalance: 32},
	}
	cur := []storage.ClusterBalance{
		{ClusterID: testClusterID(0x22), EffectiveBalance: 64},
	}

	diff := computeDiff(50, old, cur)

	require.NotNil(t, diff)
	require.Empty(t, diff.Changed)
	require.Len(t, diff.Added, 1)
	require.Equal(t, uint32(64), diff.Added[0].Balance)
	require.Len(t, diff.Removed, 1)
	require.Equal(t, uint32(32), diff.Removed[0].Balance)
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
