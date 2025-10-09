package store

// Server represents another node in the cluster.
type Server struct {
	ID       string `json:"id,omitempty"`
	Addr     string `json:"addr,omitempty"`
	Suffrage string `json:"suffrage,omitempty"`
}

// NewServer returns an initialized Server.
func NewServer(id, addr string, voter bool) *Server {
	// TODO: Implementation truncated
	return nil
}

// Servers is a set of Servers.
type Servers []*Server

// IsReadOnly returns whether the given node, as specified by its Raft ID,
// is a read-only (non-voting) node. If no node is found with the given ID
// then found will be false.
func (s Servers) IsReadOnly(id string) (readOnly bool, found bool) {
	// TODO: Implementation truncated
	return false, false
}

// Contains returns whether the given node, as specified by its Raft ID,
// is a member of the set of servers.
func (s Servers) Contains(id string) bool {
	// TODO: Implementation truncated
	return false
}

func (s Servers) Less(i, j int) bool { return s[i].ID < s[j].ID }
func (s Servers) Len() int           { return len(s) }
func (s Servers) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
