package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ssvlabs/ssv-oracle/logger"
	"github.com/ssvlabs/ssv-oracle/storage"
)

const internalError = "internal error"

func (s *Server) handleGetCommit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	commit, prevEpoch, nextEpoch, ok := s.resolveCommit(ctx, w, r)
	if !ok {
		return
	}

	resp := CommitResponse{
		Epoch:          commit.TargetEpoch,
		Status:         string(commit.Status),
		ReferenceBlock: commit.ReferenceBlock,
		MerkleRoot:     toHex(commit.MerkleRoot),
		TxHash:         toHexOrEmpty(commit.TxHash),
		PreviousEpoch:  prevEpoch,
		NextEpoch:      nextEpoch,
	}

	if strings.ToLower(r.URL.Query().Get("full")) == "true" {
		tree := buildTree(commit.ClusterBalances)

		if !rootMatches(tree.Root, commit.MerkleRoot) {
			logger.Errorw("Merkle root mismatch", "computed", toHex(tree.Root[:]), "stored", toHex(commit.MerkleRoot))
			s.writeError(w, http.StatusInternalServerError, internalError)
			return
		}

		leaves := tree.Leaves()

		clusterInfos, infoErr := s.storage.GetAllClusterInfo(ctx)
		if infoErr != nil {
			logger.Warnw("Failed to get cluster info", "error", infoErr)
		}

		resp.Clusters = make([]Cluster, len(leaves))
		for i, leaf := range leaves {
			c := Cluster{
				ClusterID:        toHex(leaf.ClusterID[:]),
				EffectiveBalance: leaf.EffectiveBalance,
				Hash:             toHex(leaf.Hash[:]),
			}
			if infoErr == nil {
				if info, ok := clusterInfos[fmt.Sprintf("%x", leaf.ClusterID[:])]; ok {
					c.OwnerAddress = toHex(info.OwnerAddress)
					c.OperatorIDs = info.OperatorIDs
				}
			}
			resp.Clusters[i] = c
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

		var total uint64
		for _, bal := range commit.ClusterBalances {
			total += uint64(bal.EffectiveBalance)
		}
		resp.TotalEffectiveBalance = total

		if prevEpoch != nil {
			prevCommit, _, _, prevErr := s.storage.GetCommitByEpoch(ctx, *prevEpoch)
			if prevErr != nil {
				logger.Warnw("Failed to get previous commit for diff", "epoch", *prevEpoch, "error", prevErr)
			} else if prevCommit != nil {
				resp.BalanceDiff = computeDiff(*prevEpoch, prevCommit.ClusterBalances, commit.ClusterBalances)
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

	commit, _, _, ok := s.resolveCommit(r.Context(), w, r)
	if !ok {
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

// resolveCommit parses the optional epoch query param and returns the commit
// with navigation epochs. Writes an error response and returns ok=false on failure.
func (s *Server) resolveCommit(ctx context.Context, w http.ResponseWriter, r *http.Request) (commit *storage.OracleCommit, prev, next *uint64, ok bool) {
	var err error

	if epochStr := r.URL.Query().Get("epoch"); epochStr != "" {
		epoch, parseErr := strconv.ParseUint(epochStr, 10, 64)
		if parseErr != nil {
			s.writeError(w, http.StatusBadRequest, "invalid epoch parameter")
			return nil, nil, nil, false
		}
		commit, prev, next, err = s.storage.GetCommitByEpoch(ctx, epoch)
	} else {
		commit, err = s.storage.GetLatestCommit(ctx)
		if err == nil && commit != nil {
			_, prev, next, _ = s.storage.GetCommitByEpoch(ctx, commit.TargetEpoch)
		}
	}

	if err != nil {
		logger.Errorw("Failed to get commit", "error", err)
		s.writeError(w, http.StatusInternalServerError, internalError)
		return nil, nil, nil, false
	}
	if commit == nil {
		s.writeError(w, http.StatusNotFound, "no commit found")
		return nil, nil, nil, false
	}
	return commit, prev, next, true
}

// parseClusterID validates and decodes a cluster ID string (0x + 64 hex chars).
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

func computeDiff(prevEpoch uint64, old, cur []storage.ClusterBalance) *CommitDiff {
	oldMap := make(map[string]uint32, len(old))
	for _, b := range old {
		oldMap[fmt.Sprintf("%x", b.ClusterID)] = b.EffectiveBalance
	}

	diff := &CommitDiff{PreviousEpoch: prevEpoch}
	for _, b := range cur {
		id := fmt.Sprintf("%x", b.ClusterID)
		if oldBal, ok := oldMap[id]; ok && oldBal != b.EffectiveBalance {
			diff.Changed = append(diff.Changed, ClusterDiff{
				ClusterID:  "0x" + id,
				OldBalance: oldBal,
				NewBalance: b.EffectiveBalance,
			})
		}
	}

	if len(diff.Changed) == 0 {
		return nil
	}
	return diff
}
