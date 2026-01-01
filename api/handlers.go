package api

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"strings"

	"ssv-oracle/logger"
)

func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	commit, err := s.storage.GetLatestCommit(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if commit == nil {
		s.writeError(w, http.StatusNotFound, "no commit found")
		return
	}

	resp := CommitResponse{
		Epoch:          commit.TargetEpoch,
		ReferenceBlock: commit.ReferenceBlock,
		MerkleRoot:     toHex(commit.MerkleRoot),
		TxHash:         toHexOrEmpty(commit.TxHash),
	}

	if r.URL.Query().Get("full") == "true" {
		tree := buildTree(commit.ClusterBalances)

		// Verify computed root matches stored root
		if !rootMatches(tree.Root, commit.MerkleRoot) {
			logger.Errorw("Merkle root mismatch", "computed", toHex(tree.Root[:]), "stored", toHex(commit.MerkleRoot))
			s.writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		leaves := tree.Leaves()

		resp.Clusters = make([]Cluster, len(leaves))
		for i, leaf := range leaves {
			resp.Clusters[i] = Cluster{
				ClusterID:        toHex(leaf.ClusterID[:]),
				EffectiveBalance: leaf.EffectiveBalance,
				Hash:             toHex(leaf.Hash[:]),
			}
		}

		innerLayers := tree.InnerLayers()
		if len(innerLayers) > 0 {
			resp.Layers = make([][]string, len(innerLayers))
			for i, layer := range innerLayers {
				resp.Layers[i] = make([]string, len(layer))
				for j, hash := range layer {
					resp.Layers[i][j] = toHex(hash[:])
				}
			}
		}
	}

	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetProof(w http.ResponseWriter, r *http.Request) {
	clusterIDStr := r.PathValue("clusterId")
	if !isValidClusterID(clusterIDStr) {
		s.writeError(w, http.StatusBadRequest, "invalid clusterId format")
		return
	}

	clusterID, _ := parseClusterID(clusterIDStr)

	commit, err := s.storage.GetLatestCommit(r.Context())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if commit == nil {
		s.writeError(w, http.StatusNotFound, "no commit found")
		return
	}

	tree := buildTree(commit.ClusterBalances)

	// Verify computed root matches stored root
	if !rootMatches(tree.Root, commit.MerkleRoot) {
		logger.Errorw("Merkle root mismatch", "computed", toHex(tree.Root[:]), "stored", toHex(commit.MerkleRoot))
		s.writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	proof, err := tree.GetProof(clusterID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	var effectiveBalance uint32
	for _, leaf := range tree.Leaves() {
		if leaf.ClusterID == clusterID {
			effectiveBalance = leaf.EffectiveBalance
			break
		}
	}

	proofStrings := make([]string, len(proof))
	for i, hash := range proof {
		proofStrings[i] = toHex(hash[:])
	}

	resp := ProofResponse{
		ClusterID:        clusterIDStr,
		EffectiveBalance: effectiveBalance,
		Proof:            proofStrings,
		MerkleRoot:       toHex(commit.MerkleRoot),
		ReferenceBlock:   commit.ReferenceBlock,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "UI not available", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// isValidClusterID validates the cluster ID format (0x-prefixed 32-byte hex).
func isValidClusterID(id string) bool {
	if len(id) != 66 || !strings.HasPrefix(id, "0x") {
		return false
	}
	_, err := hex.DecodeString(id[2:])
	return err == nil
}

// parseClusterID parses a validated cluster ID string into bytes.
func parseClusterID(id string) ([32]byte, error) {
	var result [32]byte
	decoded, err := hex.DecodeString(id[2:])
	if err != nil {
		return result, err
	}
	copy(result[:], decoded)
	return result, nil
}

// toHex converts bytes to 0x-prefixed hex string.
func toHex(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}

// toHexOrEmpty returns 0x-prefixed hex string, or empty string if nil/empty.
func toHexOrEmpty(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return "0x" + hex.EncodeToString(b)
}

// rootMatches compares a [32]byte root with a []byte slice.
func rootMatches(computed [32]byte, stored []byte) bool {
	return bytes.Equal(computed[:], stored)
}
