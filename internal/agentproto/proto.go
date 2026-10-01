package agentproto

import (
	"encoding/json"

	"continuum/internal/disk"
)

// Protocol version negotiated in Hello/Welcome (FR-N06: one minor of backward compatibility).
const (
	Protocol    = 1
	MinProtocol = 1

	ConnectProcedure = "/continuum.agent.v1.AgentService/Connect"
	EnrollPath       = "/agent/v1/enroll" // TLS, one-time token (no client cert yet)
	RenewPath        = "/agent/v1/renew"  // mTLS
	BlobPath         = "/agent/v1/blobs/" // mTLS: GET = agent downloads, PUT = agent uploads
	PeerPath         = "/peer/v1/file"    // agent ↔ agent (mTLS + controller ticket): GET ?path=&offset=
	TicketHeader     = "X-Continuum-Ticket"
	SumTrailer       = "X-Continuum-Blake3" // BLAKE3 of the whole source file, sent as an HTTP trailer

	HeartbeatSeconds = 10
	MissedBeats      = 3 // FR-N05: 3 missed heartbeats → NODE_OFFLINE
)

// AgentMsg is sent agent → controller on the Connect stream (exactly one field set).
type AgentMsg struct {
	Hello     *Hello     `json:"hello,omitempty"`
	Heartbeat *Heartbeat `json:"heartbeat,omitempty"`
	Inventory *Inventory `json:"inventory,omitempty"`
	Result    *Result    `json:"result,omitempty"`
	Progress  *Progress  `json:"progress,omitempty"`
}

// Progress reports how far a long command (pull) is, while it runs.
type Progress struct {
	ID    string `json:"id"`    // command id
	Bytes int64  `json:"bytes"` // bytes of the file present at the target so far
}

// ControllerMsg is sent controller → agent.
type ControllerMsg struct {
	Welcome *Welcome `json:"welcome,omitempty"`
	Command *Command `json:"command,omitempty"`
}

type Hello struct {
	NodeID       string   `json:"node_id"`
	Hostname     string   `json:"hostname"`
	OS           string   `json:"os"`
	OSVersion    string   `json:"os_version"`
	Arch         string   `json:"arch"`
	Version      string   `json:"version"`
	Protocol     int      `json:"protocol"`
	Capabilities []string `json:"capabilities"`
	PeerURL      string   `json:"peer_url,omitempty"` // direct agent ↔ agent endpoint (D36), empty = relay only
}

type Welcome struct {
	NodeID       string   `json:"node_id"`
	Protocol     int      `json:"protocol"`
	HeartbeatSec int      `json:"heartbeat_sec"`
	Watch        []string `json:"watch"` // data roots / mount paths the agent reports in heartbeats
}

// PathStat is the capacity of one watched path (heartbeat payload, FR-D12 for remote disks).
type PathStat struct {
	Path   string        `json:"path"`
	Exists bool          `json:"exists"`
	Cap    disk.Capacity `json:"capacity"`
}

type Heartbeat struct {
	TS    string     `json:"ts"`
	Paths []PathStat `json:"paths"`
}

type Inventory struct {
	Disks []disk.Descriptor `json:"disks"`
	Roots []string          `json:"roots"` // folders offered with --root
}

// Command asks the agent to run one operation; the agent answers with a Result of the same ID.
type Command struct {
	ID          string          `json:"id"`
	Op          string          `json:"op"`
	Args        json.RawMessage `json:"args,omitempty"`
	TraceParent string          `json:"traceparent,omitempty"` // W3C trace context of the caller (OTel)
}

type Result struct {
	ID    string          `json:"id"`
	Error string          `json:"error,omitempty"`
	Kind  string          `json:"kind,omitempty"` // not_exist | exist | no_space | invalid
	Data  json.RawMessage `json:"data,omitempty"`
}

// Operation names.
const (
	OpStat      = "stat"      // PathArgs → FileInfo (lstat semantics)
	OpReadDir   = "readdir"   // PathArgs → []FileInfo
	OpWalk      = "walk"      // PathArgs → []WalkEntry (relative, slash separated)
	OpMkdirAll  = "mkdirall"  // PathArgs
	OpRemove    = "remove"    // PathArgs
	OpRemoveAll = "removeall" // PathArgs
	OpRename    = "rename"    // RenameArgs
	OpSymlink   = "symlink"   // RenameArgs (From = target, To = link)
	OpChtimes   = "chtimes"   // ChtimesArgs
	OpHash      = "hash"      // PathArgs → HashResult (BLAKE3 where the data is)
	OpPartial   = "phash"     // PathArgs → HashResult (head+tail, duplicate finder)
	OpStatFS    = "statfs"    // PathArgs → disk.Capacity
	OpReadFile  = "readfile"  // PathArgs → []byte (small files only, e.g. markers)
	OpWriteFile = "writefile" // WriteFileArgs (small files, atomic)
	OpPut       = "put"       // PutArgs → PutResult (download blob → atomic file)
	OpGet       = "get"       // GetArgs (upload file → blob)
	OpProbe     = "probe"     // ProbeArgs → ProbeResult (preflight + identity, FR-D04)
	OpDiscover  = "discover"  // → []disk.Descriptor
	OpSmart     = "smart"     // PathArgs → disk.Smart
	OpBench     = "bench"     // BenchArgs → disk.Bench
	OpWipe      = "wipe"      // WipeArgs → WipeResult
	OpPull      = "pull"      // PullArgs → PullResult (copy from a peer agent or this node, resumable)
	OpCancel    = "cancel"    // CancelArgs: stop a running command (pause / cancel of a pull)
)

// PullArgs: the receiving agent fetches Source from the peer at Peer (empty = Source is on this node) into
// Partial, resuming from Partial's current length, verifies, applies mode/mtime and renames to Final.
type PullArgs struct {
	Peer     string `json:"peer,omitempty"`      // https://host:port of the source agent
	PeerNode string `json:"peer_node,omitempty"` // node id the peer certificate must carry
	Ticket   string `json:"ticket,omitempty"`    // controller-signed permission (see pki.Ticket)
	Source   string `json:"source"`
	Final    string `json:"final"`
	Partial  string `json:"partial"`
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`
	MtimeNs  int64  `json:"mtime_ns"`
	Verify   bool   `json:"verify"`
	BwLimit  int64  `json:"bwlimit,omitempty"` // bytes/s
}

type PullResult struct {
	Bytes     int64  `json:"bytes"`      // bytes transferred in this attempt
	Resumed   int64  `json:"resumed"`    // offset the attempt started at
	Checksum  string `json:"checksum"`   // BLAKE3 re-read from the target disk (Verify)
	SourceSum string `json:"source_sum"` // BLAKE3 computed by the source while serving
	Direct    bool   `json:"direct"`     // bytes went agent → agent (false = same node)
}

type CancelArgs struct {
	ID string `json:"id"`
}

type PathArgs struct {
	Path string `json:"path"`
}

type RenameArgs struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type ChtimesArgs struct {
	Path    string `json:"path"`
	MtimeNs int64  `json:"mtime_ns"`
}

type FileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"` // fs.FileMode bits
	MtimeNs int64  `json:"mtime_ns"`
	IsDir   bool   `json:"is_dir"`
	Link    string `json:"link,omitempty"`
}

type WalkEntry struct {
	Rel string `json:"rel"`
	FileInfo
}

type HashResult struct {
	Sum string `json:"sum"`
}

type WriteFileArgs struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
	Mode uint32 `json:"mode"`
}

// PutArgs: download blob BlobID into Partial, fsync, optionally verify by re-reading, rename to Final.
type PutArgs struct {
	BlobID  string `json:"blob_id"`
	Final   string `json:"final"`
	Partial string `json:"partial"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	MtimeNs int64  `json:"mtime_ns"`
	Verify  bool   `json:"verify"`
}

type PutResult struct {
	Bytes    int64  `json:"bytes"`
	Checksum string `json:"checksum"` // BLAKE3 of bytes written (re-read from disk when Verify)
}

// GetArgs: upload Path from Offset to blob BlobID.
type GetArgs struct {
	BlobID string `json:"blob_id"`
	Path   string `json:"path"`
}

type ProbeArgs struct {
	Path         string `json:"path"`
	MinFreeBytes uint64 `json:"min_free_bytes"`
	ControllerID string `json:"controller_id"`
	VolumeID     string `json:"volume_id"`
}

type ProbeResult struct {
	Report   disk.Report     `json:"report"`
	Identity string          `json:"identity"`
	Meta     disk.Descriptor `json:"meta"`
}

type BenchArgs struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type WipeArgs struct {
	Path   string `json:"path"`
	Passes int    `json:"passes"`
}

type WipeResult struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

// Enrollment (FR-N01).
type EnrollRequest struct {
	Token     string `json:"token"`
	PublicKey string `json:"public_key"` // PEM
	Hostname  string `json:"hostname"`
	OS        string `json:"os"`
	Version   string `json:"version"`
}

type EnrollResponse struct {
	NodeID   string `json:"node_id"`
	CertPEM  string `json:"cert_pem"`
	CAPEM    string `json:"ca_pem"`
	NotAfter string `json:"not_after"`
}

type RenewRequest struct {
	PublicKey string `json:"public_key"`
}
