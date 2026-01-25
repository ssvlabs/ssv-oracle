package api

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/ssvlabs/ssv-oracle/logger"
)

const internalError = "internal error"

func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	commit, err := s.storage.GetLatestCommit(r.Context())
	if err != nil {
		logger.Errorw("Failed to get latest commit", "error", err)
		s.writeError(w, http.StatusInternalServerError, internalError)
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

	if strings.ToLower(r.URL.Query().Get("full")) == "true" {
		tree := buildTree(commit.ClusterBalances)

		// Verify computed root matches stored root
		if !rootMatches(tree.Root, commit.MerkleRoot) {
			logger.Errorw("Merkle root mismatch", "computed", toHex(tree.Root[:]), "stored", toHex(commit.MerkleRoot))
			s.writeError(w, http.StatusInternalServerError, internalError)
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
	clusterID, err := parseClusterID(r.PathValue("clusterId"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid clusterId format")
		return
	}

	commit, err := s.storage.GetLatestCommit(r.Context())
	if err != nil {
		logger.Errorw("Failed to get latest commit", "error", err)
		s.writeError(w, http.StatusInternalServerError, internalError)
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
		s.writeError(w, http.StatusInternalServerError, internalError)
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
		ClusterID:        toHex(clusterID[:]),
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

// parseClusterID parses and validates a cluster ID string (0x + 64 hex chars).
func parseClusterID(id string) ([32]byte, error) {
	var result [32]byte
	if len(id) != 66 || !strings.HasPrefix(id, "0x") {
		return result, errors.New("invalid cluster ID format")
	}
	decoded, err := hex.DecodeString(id[2:])
	if err != nil {
		return result, errors.New("invalid cluster ID format")
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
