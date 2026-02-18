package api

// CommitResponse contains a commit with optional navigation and diff data.
type CommitResponse struct {
	Epoch                 uint64      `json:"epoch"`
	Status                string      `json:"status"`
	ReferenceBlock        uint64      `json:"referenceBlock"`
	MerkleRoot            string      `json:"merkleRoot"`
	TxHash                string      `json:"txHash"`
	Clusters              []Cluster   `json:"clusters,omitempty"`
	Layers                [][]string  `json:"layers,omitempty"`
	TotalEffectiveBalance uint64      `json:"totalEffectiveBalance,omitempty"`
	BalanceDiff           *CommitDiff `json:"balanceDiff,omitempty"`
	PreviousEpoch         *uint64     `json:"previousEpoch,omitempty"`
	NextEpoch             *uint64     `json:"nextEpoch,omitempty"`
}

// Cluster represents a cluster with its balance and leaf hash.
type Cluster struct {
	ClusterID        string   `json:"clusterId"`
	EffectiveBalance uint32   `json:"effectiveBalance"`
	Hash             string   `json:"hash,omitempty"`
	OwnerAddress     string   `json:"ownerAddress,omitempty"`
	OperatorIDs      []uint64 `json:"operatorIds,omitempty"`
}

// ProofResponse contains the merkle proof for a cluster.
type ProofResponse struct {
	ClusterID        string   `json:"clusterId"`
	EffectiveBalance uint32   `json:"effectiveBalance"`
	Proof            []string `json:"proof"`
	MerkleRoot       string   `json:"merkleRoot"`
	ReferenceBlock   uint64   `json:"referenceBlock"`
}

// CommitDiff represents cluster balance changes between two commits.
type CommitDiff struct {
	PreviousEpoch uint64        `json:"previousEpoch"`
	Changed       []ClusterDiff `json:"changed,omitempty"`
}

// ClusterDiff represents a cluster whose balance changed between commits.
type ClusterDiff struct {
	ClusterID  string `json:"clusterId"`
	OldBalance uint32 `json:"oldBalance"`
	NewBalance uint32 `json:"newBalance"`
}

// ErrorResponse is returned for all error cases.
type ErrorResponse struct {
	Error string `json:"error"`
}
