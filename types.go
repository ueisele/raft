package raft

// State represents the server state in Raft
type State int

const (
	Follower State = iota
	Candidate
	Leader
)

func (s State) String() string {
	switch s {
	case Follower:
		return "Follower"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	default:
		return "Unknown"
	}
}

// LogEntry represents a single entry in the replicated log
// Following the Raft paper's naming convention
type LogEntry struct {
	Term    int         `json:"term"`    // Term when entry was received by leader
	Index   int         `json:"index"`   // Position in the log (1-indexed)
	Command interface{} `json:"command"` // Command for state machine
}

// RequestVoteArgs represents arguments for RequestVote RPC
// Following Figure 2 in the Raft paper
type RequestVoteArgs struct {
	Term         int `json:"term"`         // Candidate's term
	CandidateID  int `json:"candidateId"`  // Candidate requesting vote
	LastLogIndex int `json:"lastLogIndex"` // Index of candidate's last log entry
	LastLogTerm  int `json:"lastLogTerm"`  // Term of candidate's last log entry
}

// RequestVoteReply represents reply for RequestVote RPC
type RequestVoteReply struct {
	Term        int  `json:"term"`        // CurrentTerm, for candidate to update itself
	VoteGranted bool `json:"voteGranted"` // True means candidate received vote
}

// AppendEntriesArgs represents arguments for AppendEntries RPC
// Following Figure 2 in the Raft paper
type AppendEntriesArgs struct {
	Term         int        `json:"term"`         // Leader's term
	LeaderID     int        `json:"leaderId"`     // So follower can redirect clients
	PrevLogIndex int        `json:"prevLogIndex"` // Index of log entry immediately preceding new ones
	PrevLogTerm  int        `json:"prevLogTerm"`  // Term of prevLogIndex entry
	Entries      []LogEntry `json:"entries"`      // Log entries to store (empty for heartbeat)
	LeaderCommit int        `json:"leaderCommit"` // Leader's commitIndex
}

// AppendEntriesReply represents reply for AppendEntries RPC
type AppendEntriesReply struct {
	Term    int  `json:"term"`    // CurrentTerm, for leader to update itself
	Success bool `json:"success"` // True if follower contained entry matching prevLogIndex and prevLogTerm
}

// InstallSnapshotArgs represents arguments for InstallSnapshot RPC
// Following Figure 13 in the Raft paper
type InstallSnapshotArgs struct {
	Term              int    `json:"term"`              // Leader's term
	LeaderID          int    `json:"leaderId"`          // So follower can redirect clients
	LastIncludedIndex int    `json:"lastIncludedIndex"` // The snapshot replaces all entries up through and including this index
	LastIncludedTerm  int    `json:"lastIncludedTerm"`  // Term of lastIncludedIndex
	Offset            int    `json:"offset"`            // Byte offset where chunk is positioned in the snapshot file
	Data              []byte `json:"data"`              // Raw bytes of the snapshot chunk, starting at offset
	Done              bool   `json:"done"`              // True if this is the last chunk
}

// InstallSnapshotReply represents reply for InstallSnapshot RPC
type InstallSnapshotReply struct {
	Term int `json:"term"` // CurrentTerm, for leader to update itself
}

// Server represents a server in the Raft cluster
type Server struct {
	ID        int    `json:"id"`
	Address   string `json:"address"`
	NonVoting bool   `json:"nonVoting,omitempty"` // For learner/non-voting members
}

// Configuration represents the cluster configuration
// Used for membership changes (Section 6 of Raft paper)
type Configuration struct {
	Servers []Server `json:"servers"`
}

// ConfigurationChange represents a configuration change entry in the log
type ConfigurationChange struct {
	Type      string        `json:"type"`      // "add_server", "remove_server", "joint", "new"
	OldConfig Configuration `json:"oldConfig"` // C_old
	NewConfig Configuration `json:"newConfig"` // C_new
}

// Snapshot represents a point-in-time snapshot of the state machine
type Snapshot struct {
	Data              []byte `json:"data"`              // The snapshot data
	LastIncludedIndex int    `json:"lastIncludedIndex"` // Last index in the snapshot
	LastIncludedTerm  int    `json:"lastIncludedTerm"`  // Term of last index
}

// PersistentState represents the persistent state that must survive crashes
// Following Section 5.2 of the Raft paper
type PersistentState struct {
	CurrentTerm int        `json:"currentTerm"` // Latest term server has seen
	VotedFor    *int       `json:"votedFor"`    // CandidateId that received vote in current term (null if none)
	Log         []LogEntry `json:"log"`         // Log entries
	CommitIndex int        `json:"commitIndex"` // Highest log entry known to be committed
}

// ConfigCommand represents a configuration change command
type ConfigCommand struct {
	Type string `json:"type"` // "configuration_change"
	Data []byte `json:"data"` // Marshaled ConfigurationChange
}
