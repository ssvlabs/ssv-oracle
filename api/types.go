package api

// CommitResponse contains the latest confirmed commit.
type CommitResponse struct {
	Epoch          uint64     `json:"epoch"`
	ReferenceBlock uint64     `json:"referenceBlock"`
	MerkleRoot     string     `json:"merkleRoot"`
	TxHash         string     `json:"txHash"`
	Clusters       []Cluster  `json:"clusters,omitempty"`
	Layers         [][]string `json:"layers,omitempty"`
}

// Cluster represents a cluster with its balance and leaf hash.
type Cluster struct {
	ClusterID        string `json:"clusterId"`
	EffectiveBalance uint32 `json:"effectiveBalance"`
	Hash             string `json:"hash"`
}

// ProofResponse contains the merkle proof for a cluster.
type ProofResponse struct {
	ClusterID        string   `json:"clusterId"`
	EffectiveBalance uint32   `json:"effectiveBalance"`
	Proof            []string `json:"proof"`
	MerkleRoot       string   `json:"merkleRoot"`
	ReferenceBlock   uint64   `json:"referenceBlock"`
}

// ErrorResponse is returned for all error cases.
type ErrorResponse struct {
	Error string `json:"error"`
}
