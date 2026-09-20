package auth

import (
	"context"
	"net"
	"net/http"
	"time"

	"traceiq/internal/model"
)

// Role is DR-25 §25.1, verbatim. Roles are a capability matrix, not a
// hierarchy (DR-25 §25.2): admin does NOT structurally imply approver,
// operator or viewer in code — the matrix in DR-25 §25.2 is the sole
// source of truth, enforced by Authorizer.Can.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleApprover Role = "approver"
	RoleAdmin    Role = "admin"
)

// SubjectKind is DR-25 §25.1, verbatim.
type SubjectKind uint8

const (
	SubjToken    SubjectKind = 1
	SubjOIDC     SubjectKind = 2
	SubjMTLS     SubjectKind = 3
	SubjChat     SubjectKind = 4
	SubjInternal SubjectKind = 5
)

// ChatIdentity is DR-25 §25.1, verbatim.
type ChatIdentity struct {
	Platform, WorkspaceID, PlatformUserID string
}

// Subject is DR-25 §25.1, verbatim.
type Subject struct {
	ID     string // "tok_<id>" | "oidc:<sub>" | "mtls:<cn>" | "rca:<investigationID>"
	Tenant model.TenantID
	Roles  []Role
	Scopes []string // "ingest" is a SCOPE, never a role
	Kind   SubjectKind

	ChatRef             *ChatIdentity
	IssuedAt, ExpiresAt time.Time
}

// Principal is DR-25 §25.1, verbatim: X-SEC's auth.Principal becomes an
// alias for one release, then is deleted.
type Principal = Subject

// ChatRequest is the inbound payload AuthenticateChat resolves through
// IdentityStore (DR-25 §25.4). The register describes its fields via
// prose ("(platform, workspace_id, platform_user_id) resolves ... to a
// provisioned Subject") but never prints a dedicated Go block for it; this
// is a minimal reconstruction. See docs/reports/w1-scaffold.md.
type ChatRequest struct {
	Platform, WorkspaceID, PlatformUserID string
	ChannelID                             string
	Signature                             []byte
	Body                                  []byte
}

// Authenticator is DR-25 §25.1, verbatim.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (Subject, error)
	AuthenticateChat(ctx context.Context, p ChatRequest) (Subject, error)
	Close() error
}

// Capability is the closed set of RBAC capability strings in the DR-25
// §25.2 matrix (e.g. "telemetry:read", "remediation_action:propose"). The
// register gives the matrix as a table, not a Go enum; capabilities are
// represented as a named string type so the matrix can be data (DR-25
// §25.2). See docs/reports/w1-scaffold.md.
type Capability string

// ResourceRef scopes an Authorizer.Can check to a specific tenant-owned
// resource (e.g. a namespace/target pair for remediation_action:execute).
// Reconstructed minimally from DR-25 §25.1's Can signature; the register
// does not print its fields. See docs/reports/w1-scaffold.md.
type ResourceRef struct {
	Tenant model.TenantID
	Kind   string
	ID     string
}

// Authorizer is DR-25 §25.1, verbatim.
type Authorizer interface {
	Can(ctx context.Context, s Subject, c Capability, res ResourceRef) error
	SeparationOfDuty(proposer, approver string) error
	Roles(s Subject) []Role
}

// LimitClass is DR-25 §25.1, verbatim.
type LimitClass uint8

const (
	LimitIngest  LimitClass = 1
	LimitAPI     LimitClass = 2
	LimitNL      LimitClass = 3
	LimitWebhook LimitClass = 4
	LimitChat    LimitClass = 5
	LimitMCP     LimitClass = 6
)

// Key is DR-25 §25.1, verbatim.
type Key struct {
	Tenant  model.TenantID
	Subject string
	Class   LimitClass
}

// Decision is the Allow verdict returned by RateLimiter.Allow. The
// register names it in the Allow signature (DR-25 §25.1) but does not
// print its fields; reconstructed minimally. See
// docs/reports/w1-scaffold.md.
type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// RateLimiter is DR-25 §25.1, verbatim.
type RateLimiter interface {
	Allow(ctx context.Context, k Key) (Decision, error)
	Close() error
}

// Event is one audit row appended through AuditSink.Append. The register
// discusses audit rows' content across DR-25/DR-27 in prose; the Go shape
// is reconstructed minimally here (tenant, actor, action, timestamp,
// payload, hash-chain fields per DR-27). See
// docs/reports/w1-scaffold.md.
type Event struct {
	Tenant    model.TenantID
	Actor     string
	Action    string
	Payload   []byte
	Timestamp time.Time
}

// Receipt is returned by AuditSink.Append: the durable, hash-chained
// position of the appended Event (DR-27). Reconstructed minimally. See
// docs/reports/w1-scaffold.md.
type Receipt struct {
	Seq  int64
	Hash []byte
}

// Filter scopes AuditSink.Query. Reconstructed minimally from the Query
// signature. See docs/reports/w1-scaffold.md.
type Filter struct {
	From, To time.Time
	Actor    string
	Action   string
	Cursor   string
	Limit    int
}

// VerifyReport is returned by AuditSink.VerifyChain: whether the
// hash-chain over [from, to) is intact. Reconstructed minimally. See
// docs/reports/w1-scaffold.md.
type VerifyReport struct {
	OK          bool
	BrokenAtSeq int64
}

// Anchor is DR-27's periodic external anchor of the audit hash chain.
// Reconstructed minimally from AuditSink.Anchor's signature. See
// docs/reports/w1-scaffold.md.
type Anchor struct {
	Seq        int64
	Hash       []byte
	AnchoredAt time.Time
}

// AuditSink is DR-25 §25.1, verbatim. Append is FAIL-CLOSED: an error
// refuses the operation.
type AuditSink interface {
	Append(ctx context.Context, e Event) (Receipt, error) // FAIL-CLOSED: an error refuses the operation
	Query(ctx context.Context, tid model.TenantID, f Filter) ([]Event, string, error)
	VerifyChain(ctx context.Context, tid model.TenantID, from, to int64) (VerifyReport, error)
	Anchor(ctx context.Context, tid model.TenantID) (Anchor, error) // DR-27
}

// AuditLog is DR-25 §25.1, verbatim: 02's name; X-SEC's AuditSink and
// F09's private chain are the same component.
type AuditLog = AuditSink

// SecretSource is DR-25 §25.1, verbatim.
type SecretSource interface {
	Get(ctx context.Context, ref string) ([]byte, error)          // "env:NAME" | "file:/path"
	Watch(ctx context.Context, ref string) (<-chan []byte, error) // rotation visible within 60s
}

// EgressDialer is DR-25 §25.1, verbatim: the ONLY http.Client factory
// (DR-20). internal/llm.NewClient takes the *http.Client this produces,
// rather than importing auth directly, to hold DR-2's adjacency (llm may
// import only model, config).
type EgressDialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
	HTTPClient(timeout time.Duration) *http.Client // the ONLY http.Client factory (DR-20)
}

// IdentityBinding is DR-25 §25.1, verbatim.
type IdentityBinding struct {
	Platform, WorkspaceID, PlatformUserID string
	SubjectID                             string
	Tenant                                model.TenantID
	CreatedBy                             string
	CreatedAt                             time.Time
	Disabled                              bool
}

// IdentityStore is DR-25 §25.1, verbatim.
type IdentityStore interface {
	Resolve(ctx context.Context, platform, workspaceID, platformUserID string) (Subject, error)
	Put(ctx context.Context, b IdentityBinding, by Subject) error
	List(ctx context.Context, tid model.TenantID) ([]IdentityBinding, error)
	Delete(ctx context.Context, tid model.TenantID, platform, workspaceID, platformUserID string, by Subject) error
}
